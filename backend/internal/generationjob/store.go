// Package generationjob persists physical-attempt accounting and execution
// fences. UNKNOWN work is never automatically replayed or released on resume.
package generationjob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	_ "modernc.org/sqlite"
)

var ErrFence = errors.New("任务执行版本已失效")
var ErrBudget = errors.New("任务物理请求预算已用尽")

type Store struct{ db *sql.DB }
type Fence struct {
	JobID             string
	Generation, Epoch int64
}
type Budget struct{ Limit, Spent, UnknownHeld, Reserved int64 }

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("请求预算目录不可用")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "generation-jobs.db"))+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("请求预算不可用")
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS generation_jobs (
 job_id TEXT PRIMARY KEY, generation INTEGER NOT NULL, epoch INTEGER NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0, attempt_limit INTEGER NOT NULL CHECK(attempt_limit>0)
);
CREATE TABLE IF NOT EXISTS generation_attempts (
 job_id TEXT NOT NULL, attempt_id TEXT NOT NULL, generation INTEGER NOT NULL,
 epoch INTEGER NOT NULL, request_digest TEXT NOT NULL, request_bytes INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('reserved','dispatching','spent','unknown','released')),
 PRIMARY KEY(job_id,attempt_id)
);
CREATE TABLE IF NOT EXISTS generation_success_cache (
 job_id TEXT NOT NULL, cache_key TEXT NOT NULL, identity TEXT NOT NULL,
 epoch INTEGER NOT NULL,generation INTEGER NOT NULL,body BLOB NOT NULL,digest TEXT NOT NULL,
 PRIMARY KEY(job_id,cache_key)
);
CREATE TABLE IF NOT EXISTS generation_cache_adoptions (
 job_id TEXT NOT NULL,cache_key TEXT NOT NULL,generation INTEGER NOT NULL,epoch INTEGER NOT NULL,
 identity TEXT NOT NULL,digest TEXT NOT NULL,source_generation INTEGER NOT NULL,
 PRIMARY KEY(job_id,cache_key,generation,epoch)
);
CREATE TABLE IF NOT EXISTS generation_spool_reservations (
 job_id TEXT NOT NULL, artifact_key TEXT NOT NULL, bytes INTEGER NOT NULL CHECK(bytes>0),
 PRIMARY KEY(job_id,artifact_key)
);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("请求预算初始化失败")
	}
	return &Store{db: db}, nil
}
func (store *Store) Close() error { return store.db.Close() }

func (store *Store) Check(ctx context.Context, fence Fence) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("请求版本事务失败")
	}
	defer tx.Rollback()
	return check(ctx, tx, fence)
}

func (store *Store) CurrentGeneration(ctx context.Context, jobID string) (int64, error) {
	var generation int64
	err := store.db.QueryRowContext(ctx, `SELECT generation FROM generation_jobs WHERE job_id=?`, jobID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("请求版本读取失败")
	}
	return generation, nil
}

// WithFence serializes a local publication with generation rollover/revocation
// in the same SQLite write transaction. Callbacks must not re-enter this Store.
func (store *Store) WithFence(ctx context.Context, fence Fence, publish func() error) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("请求版本事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	if err := publish(); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return fmt.Errorf("请求版本提交失败")
	}
	return nil
}

// Acquire is an expected-generation CAS, including first creation expected=0.
// The limit is immutable; execution rollover cannot replenish the stable job.
func (store *Store) Acquire(ctx context.Context, jobID string, expected, limit int64) (Fence, error) {
	if jobID == "" || limit < 1 || expected < 0 {
		return Fence{}, ErrFence
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Fence{}, fmt.Errorf("请求预算事务失败")
	}
	defer tx.Rollback()
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO generation_jobs(job_id,generation,epoch,attempt_limit) VALUES (?,0,1,?)`, jobID, limit)
		if err != nil {
			return Fence{}, fmt.Errorf("请求预算创建失败")
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE generation_jobs SET generation=generation+1 WHERE job_id=? AND generation=? AND attempt_limit=? AND revoked=0`, jobID, expected, limit)
	if err != nil {
		return Fence{}, fmt.Errorf("请求版本保存失败")
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Fence{}, ErrFence
	}
	// Crashes can leave reservations/dispatches. Only never-dispatched work is
	// releasable. A superseded request remains financially unknown.
	_, err = tx.ExecContext(ctx, `UPDATE generation_attempts SET state=CASE state WHEN 'reserved' THEN 'released' ELSE 'unknown' END WHERE job_id=? AND state IN ('reserved','dispatching')`, jobID)
	if err != nil {
		return Fence{}, fmt.Errorf("请求预算恢复失败")
	}
	fence := Fence{JobID: jobID, Generation: expected + 1}
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM generation_jobs WHERE job_id=?`, jobID).Scan(&fence.Epoch); err != nil {
		return Fence{}, fmt.Errorf("请求版本读取失败")
	}
	if err := tx.Commit(); err != nil {
		return Fence{}, fmt.Errorf("请求版本提交失败")
	}
	return fence, nil
}

func check(ctx context.Context, tx *sql.Tx, fence Fence) error {
	var valid int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM generation_jobs WHERE job_id=? AND generation=? AND epoch=? AND revoked=0`, fence.JobID, fence.Generation, fence.Epoch).Scan(&valid); err != nil {
		return fmt.Errorf("请求版本检查失败")
	}
	if valid != 1 {
		return ErrFence
	}
	return nil
}

func (store *Store) Budget(ctx context.Context, jobID string) (Budget, error) {
	var budget Budget
	err := store.db.QueryRowContext(ctx, `SELECT attempt_limit,
 (SELECT count(*) FROM generation_attempts WHERE job_id=j.job_id AND state='spent'),
 (SELECT count(*) FROM generation_attempts WHERE job_id=j.job_id AND state IN ('unknown','dispatching')),
 (SELECT count(*) FROM generation_attempts WHERE job_id=j.job_id AND state='reserved')
 FROM generation_jobs j WHERE job_id=?`, jobID).Scan(&budget.Limit, &budget.Spent, &budget.UnknownHeld, &budget.Reserved)
	if err != nil {
		return Budget{}, fmt.Errorf("请求预算读取失败")
	}
	return budget, nil
}

func (store *Store) Reserve(ctx context.Context, fence Fence, attemptID, digest string, requestBytes int64) error {
	if attemptID == "" || len(digest) != 64 || requestBytes < 0 {
		return fmt.Errorf("请求预算标识无效")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("请求预算事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	var oldDigest, state string
	var oldBytes, gen, epoch int64
	err = tx.QueryRowContext(ctx, `SELECT request_digest,request_bytes,generation,epoch,state FROM generation_attempts WHERE job_id=? AND attempt_id=?`, fence.JobID, attemptID).Scan(&oldDigest, &oldBytes, &gen, &epoch, &state)
	if err == nil {
		if oldDigest != digest || oldBytes != requestBytes || gen != fence.Generation || epoch != fence.Epoch || state != "reserved" {
			return ErrFence
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("请求预算读取失败")
	}
	var remaining int64
	err = tx.QueryRowContext(ctx, `SELECT attempt_limit-(SELECT count(*) FROM generation_attempts WHERE job_id=? AND state!='released') FROM generation_jobs WHERE job_id=?`, fence.JobID, fence.JobID).Scan(&remaining)
	if err != nil {
		return fmt.Errorf("请求预算读取失败")
	}
	if remaining < 1 {
		return ErrBudget
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO generation_attempts VALUES (?,?,?,?,?,?,'reserved')`, fence.JobID, attemptID, fence.Generation, fence.Epoch, digest, requestBytes)
	if err != nil {
		return fmt.Errorf("请求预算预留失败")
	}
	if tx.Commit() != nil {
		return fmt.Errorf("请求预算提交失败")
	}
	return nil
}

func (store *Store) dispatch(ctx context.Context, fence Fence, attemptID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("请求预算事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE generation_attempts SET state='dispatching' WHERE job_id=? AND attempt_id=? AND generation=? AND epoch=? AND state='reserved'`, fence.JobID, attemptID, fence.Generation, fence.Epoch)
	if err != nil {
		return fmt.Errorf("请求派发记录失败")
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrFence
	}
	if tx.Commit() != nil {
		return fmt.Errorf("请求派发提交失败")
	}
	return nil
}

func (store *Store) finish(ctx context.Context, fence Fence, attemptID string, complete bool) error {
	// Settlement is permitted for an old generation, but never grants it write
	// or publication eligibility. Without trusted usage a full response spends
	// its attempt cap; any dispatched failure/429 holds the full cap UNKNOWN.
	state := "unknown"
	if complete {
		state = "spent"
	}
	_, err := store.db.ExecContext(ctx, `UPDATE generation_attempts SET state=CASE WHEN state='reserved' THEN 'released' ELSE ? END WHERE job_id=? AND attempt_id=? AND generation=? AND epoch=? AND state IN ('reserved','dispatching','unknown')`, state, fence.JobID, attemptID, fence.Generation, fence.Epoch)
	if err != nil {
		return fmt.Errorf("请求预算结算失败")
	}
	return nil
}

type Guard struct {
	Store *Store
	Fence Fence
}
type attempt struct {
	guard Guard
	id    string
}

func (guard Guard) Reserve(ctx context.Context, digest string, requestBytes int64) (contract.PhysicalRequestAttempt, error) {
	id := ids.New()
	if err := guard.Store.Reserve(ctx, guard.Fence, id, digest, requestBytes); err != nil {
		return nil, err
	}
	return &attempt{guard: guard, id: id}, nil
}
func (value *attempt) Dispatch(ctx context.Context) error {
	return value.guard.Store.dispatch(ctx, value.guard.Fence, value.id)
}
func (value *attempt) Finish(ctx context.Context, complete bool) error {
	return value.guard.Store.finish(ctx, value.guard.Fence, value.id, complete)
}

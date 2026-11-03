package generationjob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

const MaxSuccessCacheBytes = 4 << 20
const MaxJobSuccessCacheBytes = 64 << 20

type ScopedCache struct {
	Store *Store
	Fence Fence
}

func (cache ScopedCache) Save(ctx context.Context, key, identity string, body []byte) error {
	return cache.Store.SaveSuccess(ctx, cache.Fence, key, identity, body)
}
func (cache ScopedCache) Adopt(ctx context.Context, key, identity string) ([]byte, bool, error) {
	return cache.Store.AdoptSuccess(ctx, cache.Fence, key, identity)
}

func cacheDigest(body []byte) string { hash := sha256.Sum256(body); return hex.EncodeToString(hash[:]) }

// SaveSuccess is only for fully validated local successes. UNKNOWN attempts
// have no cache entry. A cache record does not advance the ordered checkpoint.
func (store *Store) SaveSuccess(ctx context.Context, fence Fence, key, identity string, body []byte) error {
	if key == "" || identity == "" || len(body) > MaxSuccessCacheBytes {
		return fmt.Errorf("成功结果缓存超过范围")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("成功结果缓存事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	var used int64
	if tx.QueryRowContext(ctx, `SELECT coalesce(sum(length(body)),0) FROM generation_success_cache WHERE job_id=? AND cache_key!=?`, fence.JobID, key).Scan(&used) != nil {
		return fmt.Errorf("成功结果缓存读取失败")
	}
	if used+int64(len(body)) > MaxJobSuccessCacheBytes {
		return fmt.Errorf("任务成功结果缓存空间已满")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO generation_success_cache(job_id,cache_key,identity,epoch,generation,body,digest) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id,cache_key) DO UPDATE SET identity=excluded.identity,epoch=excluded.epoch,generation=excluded.generation,body=excluded.body,digest=excluded.digest`, fence.JobID, key, identity, fence.Epoch, fence.Generation, body, cacheDigest(body))
	if err != nil {
		return fmt.Errorf("成功结果缓存保存失败")
	}
	if tx.Commit() != nil {
		return fmt.Errorf("成功结果缓存提交失败")
	}
	return nil
}

// AdoptSuccess explicitly changes the execution owner after checking current
// source/plan/recipe identity and epoch. Hash equality alone is insufficient.
func (store *Store) AdoptSuccess(ctx context.Context, fence Fence, key, identity string) ([]byte, bool, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("成功结果缓存事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return nil, false, err
	}
	var body []byte
	var hash string
	var size int64
	var sourceGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT length(body),digest,generation FROM generation_success_cache WHERE job_id=? AND cache_key=? AND identity=? AND epoch=?`, fence.JobID, key, identity, fence.Epoch).Scan(&size, &hash, &sourceGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil || size < 0 || size > MaxSuccessCacheBytes {
		return nil, false, fmt.Errorf("成功结果缓存无效")
	}
	if tx.QueryRowContext(ctx, `SELECT body FROM generation_success_cache WHERE job_id=? AND cache_key=? AND identity=? AND epoch=?`, fence.JobID, key, identity, fence.Epoch).Scan(&body) != nil || int64(len(body)) != size || cacheDigest(body) != hash {
		return nil, false, fmt.Errorf("成功结果缓存已损坏")
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO generation_cache_adoptions(job_id,cache_key,generation,epoch,identity,digest,source_generation) VALUES(?,?,?,?,?,?,?)`, fence.JobID, key, fence.Generation, fence.Epoch, identity, hash, sourceGeneration); err != nil {
		return nil, false, fmt.Errorf("成功缓存接管回执保存失败")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE generation_success_cache SET generation=? WHERE job_id=? AND cache_key=? AND identity=? AND epoch=?`, fence.Generation, fence.JobID, key, identity, fence.Epoch); err != nil {
		return nil, false, fmt.Errorf("成功结果缓存接管失败")
	}
	if tx.Commit() != nil {
		return nil, false, fmt.Errorf("成功结果缓存接管提交失败")
	}
	return body, true, nil
}

// Revoke shares the exact serialization point used by WithFence publication.
// It leaves a persistent tombstone and all accounting; same hashes cannot
// restore eligibility. There is deliberately no implicit authorization reset.
func (store *Store) Revoke(ctx context.Context, fence Fence) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("撤销事务失败")
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE generation_jobs SET epoch=epoch+1,revoked=1 WHERE job_id=?`, fence.JobID); err != nil {
		return fmt.Errorf("撤销保存失败")
	}
	if tx.Commit() != nil {
		return fmt.Errorf("撤销提交失败")
	}
	return nil
}

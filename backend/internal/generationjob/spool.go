package generationjob

import (
	"context"
	"database/sql"
	"fmt"
)

// ReserveSpool commits before filesystem publication. Reservations survive
// crashes, epoch changes and generation rollover; uncertain writes never free
// space automatically. Reusing identical immutable bytes costs no extra quota.
func (store *Store) ReserveSpool(ctx context.Context, fence Fence, key string, size, limit int64) error {
	if key == "" || size <= 0 || size > limit {
		return fmt.Errorf("摘要持久分页配额超限")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := check(ctx, tx, fence); err != nil {
		return err
	}
	var prior int64
	err = tx.QueryRowContext(ctx, `SELECT bytes FROM generation_spool_reservations WHERE job_id=? AND artifact_key=?`, fence.JobID, key).Scan(&prior)
	if err == nil {
		if prior != size {
			return ErrFence
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("摘要持久分页配额读取失败")
	}
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0) FROM generation_spool_reservations WHERE job_id=?`, fence.JobID).Scan(&used); err != nil {
		return err
	}
	if used > limit-size {
		return fmt.Errorf("摘要持久分页配额超限")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO generation_spool_reservations(job_id,artifact_key,bytes) VALUES(?,?,?)`, fence.JobID, key, size); err != nil {
		return err
	}
	return tx.Commit()
}

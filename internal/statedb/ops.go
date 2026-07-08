package statedb

import "database/sql"

// Op states.
const (
	OpPlanned    = "planned"
	OpInProgress = "in_progress"
	OpDone       = "done"
)

// PendingOp is one journal row for crash resume.
type PendingOp struct {
	ID      int64
	Type    string
	RelPath string
	FileID  string
	Payload string
	State   string
}

// AddOps journals a batch of planned ops and returns their ids in order.
func (d *DB) AddOps(ops []PendingOp) ([]int64, error) {
	ids := make([]int64, 0, len(ops))
	err := d.Tx(func(tx *sql.Tx) error {
		for _, op := range ops {
			res, err := tx.Exec(`insert into pending_ops (op_type, rel_path, drive_file_id, payload, state)
				values (?, ?, ?, ?, ?)`, op.Type, op.RelPath, op.FileID, op.Payload, OpPlanned)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// SetOpState updates one op's state.
func (d *DB) SetOpState(id int64, state string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`update pending_ops set state = ? where op_id = ?`, state, id)
	return err
}

// CompleteOp marks the op done and applies the baseline mutation in the
// same transaction — the core durability contract: data first, then both
// records atomically.
func (d *DB) CompleteOp(id int64, apply func(tx *sql.Tx) error) error {
	return d.Tx(func(tx *sql.Tx) error {
		if apply != nil {
			if err := apply(tx); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`update pending_ops set state = ? where op_id = ?`, OpDone, id)
		return err
	})
}

// StaleOps returns ops left over from a previous run (anything not done).
func (d *DB) StaleOps() ([]PendingOp, error) {
	rows, err := d.sql.Query(`select op_id, op_type, coalesce(rel_path,''), coalesce(drive_file_id,''),
		coalesce(payload,''), state from pending_ops where state != ? order by op_id`, OpDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingOp
	for rows.Next() {
		var op PendingOp
		if err := rows.Scan(&op.ID, &op.Type, &op.RelPath, &op.FileID, &op.Payload, &op.State); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ClearOps removes every journal row. Called at the start of a sync after
// stale ops have been inspected (the fresh plan supersedes them) and at the
// end to drop the done rows.
func (d *DB) ClearOps() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`delete from pending_ops`)
	return err
}

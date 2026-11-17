package db

// EmailCaseDuplicates returns, lowercased, every email address held by more than one user row
// once case is ignored (#2466). Such rows are one person split into two accounts -- possibly two
// owners -- by lookups that used to match case exactly.
//
// Reported, never merged: tokens, reservations and audit history hang off a user's id, and which
// account survives is the operator's decision, not a migration's.
func (d *DB) EmailCaseDuplicates() ([]string, error) {
	rows, err := d.conn.Query(`SELECT lower(email) FROM users GROUP BY lower(email) HAVING count(*) > 1 ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

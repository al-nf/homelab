package db

import (
	"database/sql"
	"fmt"
	"time"
)

type ATSType string

const (
	ATSGreenhouse ATSType = "greenhouse"
	ATSLever      ATSType = "lever"
	ATSAshby      ATSType = "Ashby"
	ATSWorkday    ATSType = "workday"
	ATSGeneric    ATSType = "generic"
)

type Company struct {
	ID          int64
	Name        string
	Slug        string
	ATS         ATSType
	LastChecked sql.NullTime
}

type Posting struct {
	ID         int64
	CompanyID  int64
	ExternalID string
	Title      string
	URL        string
	Location   string
	PostedAt   sql.NullTime
	FoundAt    time.Time
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

type DB struct {
	db *sql.DB
}

func (d *DB) AddCompany(c Company) (int64, error) {
	res, err := d.db.Exec("INSERT INTO companies (name, slug, ats_type) VALUES (?, ?, ?)",
		c.Name, c.Slug, c.ATS)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) ListCompanies() ([]Company, error) {
	rows, err := d.db.Query("SELECT id, name, slug, ats_type, last_checked FROM companies")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var companies []Company
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ATS, &c.LastChecked); err != nil {
			return nil, err
		}
		companies = append(companies, c)
	}
	return companies, rows.Err()
}

func (d *DB) UpdateLastChecked(companyID int64) error {
	_, err := d.db.Exec(`UPDATE companies SET last_checked = ? WHERE id = ?`, time.Now(), companyID)
	return err
}

func (d *DB) UpsertPosting(p Posting) (bool, error) {
	res, err := d.db.Exec(`INSERT OR IGNORE INTO postings
		(company_id, external_id, title, url, location, posted_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		p.CompanyID, p.ExternalID, p.Title, p.URL, p.Location, p.PostedAt,
	)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (d *DB) RecentPostings(since time.Duration) ([]Posting, error) {
	rows, err := d.db.Query(`SELECT id, company_id, external_id, title, url, location, posted_at, found_at
		FROM postings WHERE found_at >= datetime('now', ?)
		ORDER BY found_at DESC`,
		fmt.Sprintf("-%d seconds", int(since.Seconds())),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var postings []Posting
	for rows.Next() {
		var p Posting
		if err := rows.Scan(
			&p.ID,
			&p.CompanyID,
			&p.ExternalID,
			&p.Title,
			&p.URL,
			&p.Location,
			&p.PostedAt,
			&p.FoundAt,
		); err != nil {
			return nil, err
		}
		postings = append(postings, p)
	}
	return postings, rows.Err()
}

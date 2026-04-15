package main

import (
	"log"
	"os"
	"time"
	"sort"
	"strings"
	"database/sql"
	"github.com/joho/godotenv"
	"github.com/al-nf/job-monitor/internal/db"
	"github.com/al-nf/job-monitor/internal/scraper"
	"github.com/al-nf/job-monitor/internal/notifier"

	_ "github.com/mattn/go-sqlite3"

)

func main() {
	godotenv.Load()
	d, err := db.Open("./jobs.db")
	if err != nil {
		log.Fatal(err)
	}
	nonUS := []string {
		"london", "uk", "dublin", "ireland", "sydney", "australia",
		"tokyo", "japan", "india", "bangalore", "germany", "munich",
		"france", "paris", "singapore", "zurich", "switzerland",
		"brussels", "belgium", "korea", "seoul", "shanghai", "shenzhen",
		"suzhou", "minato", "linz", "pohang", "yokohama", "changsha",
	}
	for {
		companies, err := d.ListCompanies()
		if err != nil {
			log.Fatal(err)
		}

		for _, company := range companies {
			s := scraper.For(company.ATS)
			postings, err := s.Fetch(company.Slug)
			if err != nil {
				log.Printf("error fetching %s: %v", company.Slug, err)
				continue
			}

			var newPostings []db.Posting
			for _, p := range postings {
				t := strings.ToLower(p.Title)
				loc := strings.ToLower(p.Location)
				skip := false
				for _, place := range nonUS {
					if strings.Contains(loc, place) {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				if !strings.Contains(t, "intern") ||
					strings.Contains(t, "internal") ||
					strings.Contains(t, "international") ||
					strings.Contains(t, "phd") ||
					strings.Contains(t, "mba") ||
					strings.Contains(t, "legal") ||
					strings.Contains(t, "marketing") ||
					strings.Contains(t, "customer") ||
					strings.Contains(t, "business") ||
					strings.Contains(t, "master") {
					continue
				}
				isNew, err := d.UpsertPosting(db.Posting{
					CompanyID: company.ID,
					ExternalID: p.ExternalID,
					Title: p.Title,
					URL: p.URL,
					Location: p.Location,
					PostedAt: sql.NullTime{Time: p.PostedAt, Valid: !p.PostedAt.IsZero()},
				})
				if err != nil {
					log.Printf("error upserting %s: %v", p.ExternalID, err)
					continue
				}
				if isNew {
					newPostings = append(newPostings, db.Posting{
						CompanyID: company.ID,
						ExternalID: p.ExternalID,
						Title: p.Title,
						URL: p.URL,
						Location: p.Location,
						PostedAt: sql.NullTime{Time: p.PostedAt, Valid: !p.PostedAt.IsZero()},
					})
				}
			}
			
			sort.Slice(newPostings, func(i, j int) bool {
				return newPostings[i].PostedAt.Time.After(newPostings[j].PostedAt.Time)
			})

			if len(newPostings) > 20 {
				newPostings = newPostings[:20]
			}
			if len(newPostings) > 0 {
				n := &notifier.WebhookNotifier{WebhookURL: os.Getenv("WEBHOOK_URL")}
				if err := n.Notify(company, newPostings); err != nil {
					log.Printf("error notifying for %s: %v", company.Name, err)
				}
			}
			if err := d.UpdateLastChecked(company.ID); err != nil {
				log.Printf("error updating last_checked for %d: %v", company.ID, err)
			}
		}
		time.Sleep(1 * time.Hour)
	}
}

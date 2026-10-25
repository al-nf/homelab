package notifier

import (
	"github.com/al-nf/job-monitor/internal/db"
	"os"
	"log"
	"fmt"
	"encoding/json"
	"net/http"
	"bytes"
)

type Notifier interface {
	Notify(company db.Company, postings []db.Posting) error
}

type WebhookNotifier struct {
	WebhookURL string
}

func (w *WebhookNotifier) Notify(company db.Company, postings []db.Posting) error {
	url := os.Getenv("WEBHOOK_URL")
	if url == "" {
		log.Fatal("WEBOOK_URL not set")
	}
	type embedField struct {
		Name string `json:"name"`
		Value string `json:"value"`
	}
	type embed struct {
		Title string `json:"title"`
		Fields []embedField `json:"fields"`
	}
	type payload struct {
		Embeds []embed `json:"embeds"`
	}
	
	var fields []embedField
	for _, p := range postings {
		date := ""
		if p.PostedAt.Valid {
			date += p.PostedAt.Time.Format("Jan 2")
		}
		fields = append(fields, embedField{
			Name: p.Title,
			Value: fmt.Sprintf("[%s](%s) - %s", p.Location, p.URL, date),
		})
	}

	body, _ := json.Marshal(payload{
		Embeds: []embed{{
			Title: fmt.Sprintf("%d new posting(s) - %s", len(postings), company.Name),
			Fields: fields,
		}},
	})
	log.Println("payload:", string(body))

	resp, err := http.Post(w.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("webhook: unexpected status %d", resp.StatusCode)
	}
	return nil
}

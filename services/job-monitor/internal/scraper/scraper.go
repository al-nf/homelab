package scraper

import (
	"net/http"
	"fmt"
	"time"
	"encoding/json"
	"github.com/al-nf/job-monitor/internal/db"
)

type Posting struct {
	ExternalID string
	Title      string
	URL        string
	Location   string
	PostedAt   time.Time
}

type Scraper interface {
	Fetch(slug string) ([]Posting, error)
}

// For returns the appropriate Scraper for the given ATS type.
func For(ats db.ATSType) Scraper {
	switch ats {
	case db.ATSGreenhouse:
		return &GreenhouseScraper{}
	case db.ATSLever:
		return &LeverScraper{}
	default:
		panic(fmt.Sprintf("no scraper for ATS type %s", ats))
	}
}

// https://boards-api.greenhouse.io/v1/boards/{slug}/jobs
type GreenhouseScraper struct{}

func (g *GreenhouseScraper) Fetch(slug string) ([]Posting, error) {
	client := &http.Client {
		Timeout: 10 * time.Second,
	}
	url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs", slug)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, slug)
	}

	var res struct {
		Jobs []struct {
			ID int64 `json:"id"`
			Title string `json:"title"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			AbsoluteURL string `json:"absolute_url"`
			UpdatedAt string `json:"updated_at"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
        return nil, err
    }

    var postings []Posting
    for _, j := range res.Jobs {
		postedAt, _ := time.Parse(time.RFC3339, j.UpdatedAt)
        postings = append(postings, Posting{
            ExternalID: fmt.Sprintf("%d", j.ID),
            Title: j.Title,
            URL: j.AbsoluteURL,
            Location: j.Location.Name,
			PostedAt: postedAt,
        })
    }
    return postings, nil
}

// https://api.lever.co/v0/postings/{slug}?mode=json
type LeverScraper struct{}

func (l *LeverScraper) Fetch(slug string) ([]Posting, error) {
	client := &http.Client {
		Timeout: 10 * time.Second,
	}
	url := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", slug)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, slug)
	}

	var res []struct {
		ID string `json:"id"`
		Title string `json:"text"`
		HostedURL string `json:"applyUrl"`
		CreatedAt int64 `json:"createdAt"`
		Categories struct {
			Location string `json:"location"`
		} `json:"categories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
        return nil, err
    }

    var postings []Posting
    for _, j := range res {
        postings = append(postings, Posting{
            ExternalID: j.ID,
            Title: j.Title,
            URL: j.HostedURL,
            Location: j.Categories.Location,
			PostedAt: time.UnixMilli(j.CreatedAt),
        })
    }
    return postings, nil
}

// https://{slug}.wd5.myworkdayjobs.com
type WorkdayScraper struct{}

func (w *WorkdayScraper) Fetch(slug string) ([]Posting, error) {
	panic("not implemented")
}

// --- Generic ---
// Fallback for companies with custom career pages.
// Requires a per-company CSS selector config to locate job listing elements.
type GenericScraper struct {
	Selector string // CSS selector for job listing elements
}

func (g *GenericScraper) Fetch(slug string) ([]Posting, error) {
	panic("not implemented")
}

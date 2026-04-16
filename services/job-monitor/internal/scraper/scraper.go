package scraper

import (
	"encoding/json"
	"fmt"
	"github.com/al-nf/job-monitor/internal/db"
	"github.com/playwright-community/playwright-go"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
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
	case db.ATSAshby:
		return &AshbyScraper{}
	case db.ATSApple:
		return &AppleScraper{}
	case db.ATSGoogle:
		return &GoogleScraper{}
	case db.ATSWorkday:
		return &WorkdayScraper{}
	default:
		panic(fmt.Sprintf("no scraper for ATS type %s", ats))
	}
}

// https://boards-api.greenhouse.io/v1/boards/{slug}/jobs
type GreenhouseScraper struct{}

func (g *GreenhouseScraper) Fetch(slug string) ([]Posting, error) {
	client := &http.Client{
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
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			AbsoluteURL string `json:"absolute_url"`
			UpdatedAt   string `json:"updated_at"`
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
			Title:      j.Title,
			URL:        j.AbsoluteURL,
			Location:   j.Location.Name,
			PostedAt:   postedAt,
		})
	}
	return postings, nil
}

// https://api.lever.co/v0/postings/{slug}?mode=json
type LeverScraper struct{}

func (l *LeverScraper) Fetch(slug string) ([]Posting, error) {
	client := &http.Client{
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
		ID         string `json:"id"`
		Title      string `json:"text"`
		HostedURL  string `json:"applyUrl"`
		CreatedAt  int64  `json:"createdAt"`
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
			Title:      j.Title,
			URL:        j.HostedURL,
			Location:   j.Categories.Location,
			PostedAt:   time.UnixMilli(j.CreatedAt),
		})
	}
	return postings, nil
}

// https://api.ashbyhq.com/posting-api/job-board/{slug}
type AshbyScraper struct{}

func (a *AshbyScraper) Fetch(slug string) ([]Posting, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	url := fmt.Sprintf("https://api.ashbyhq.com/posting-api/job-board/%s", slug)
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
			ID            string `json:"id"`
			Title         string `json:"title"`
			JobPostingURL string `json:"jobPostingUrl"`
			Location      string `json:"locationName"`
			PublishedAt   string `json:"publishedAt"`
		} `json:"jobPostings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	var postings []Posting
	for _, j := range res.Jobs {
		postedAt, _ := time.Parse(time.RFC3339, j.PublishedAt)
		postings = append(postings, Posting{
			ExternalID: j.ID,
			Title:      j.Title,
			URL:        j.JobPostingURL,
			Location:   j.Location,
			PostedAt:   postedAt,
		})
	}
	return postings, nil
}

// https://{slug}.wd5.myworkdayjobs.com
type WorkdayScraper struct{}

// Narrow searchText keeps each crawl under Workday's ~2000-offset pagination cap.
var workdayInternSearchTerms = []string{"intern", "internship"}

func (w *WorkdayScraper) Fetch(slug string) ([]Posting, error) {
	s := strings.TrimSpace(slug)
	if s == "" {
		return nil, fmt.Errorf("workday scraper requires non-empty slug")
	}
	client := &http.Client{Timeout: 30 * time.Second}

	// Explicit CXS jobs API URL (paste from DevTools).
	if strings.Contains(s, "/wday/cxs/") && strings.HasSuffix(s, "/jobs") &&
		(strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
		postings, err := workdayFetchViaAPIMulti(client, s, workdayInternSearchTerms)
		if err == nil && len(postings) > 0 {
			return postings, nil
		}
		return workdayFetchListingsPlaywright(s)
	}

	var endpoint string
	endpoint, err := resolveWorkdayJobsEndpoint(client, s)
	if err != nil {
		endpoint, _ = resolveWorkdayJobsEndpointPlaywright(s)
	}
	if endpoint != "" {
		postings, err := workdayFetchViaAPIMulti(client, endpoint, workdayInternSearchTerms)
		if err == nil && len(postings) > 0 {
			return postings, nil
		}
	}

	// Listing pages (e.g. https://intel.wd1.myworkdayjobs.com/External) render jobs in the DOM;
	// CXS path is often not present in static HTML.
	return workdayFetchListingsPlaywright(s)
}

func workdayFetchViaAPIMulti(client *http.Client, endpoint string, terms []string) ([]Posting, error) {
	if len(terms) == 0 {
		terms = []string{""}
	}
	seen := make(map[string]Posting)
	for _, term := range terms {
		postings, err := workdayFetchViaAPI(client, endpoint, term)
		if err != nil {
			return nil, fmt.Errorf("workday search %q: %w", term, err)
		}
		for _, p := range postings {
			if _, ok := seen[p.ExternalID]; !ok {
				seen[p.ExternalID] = p
			}
		}
	}
	out := make([]Posting, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out, nil
}

func workdayFetchViaAPI(client *http.Client, endpoint, searchText string) ([]Posting, error) {
	var postings []Posting
	limit := 20
	offset := 0

	for {
		payload := map[string]any{
			"appliedFacets": map[string][]string{},
			"limit":         limit,
			"offset":        offset,
			"searchText":    searchText,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("workday api status %d", resp.StatusCode)
		}

		var res struct {
			Total       int `json:"total"`
			JobPostings []struct {
				ID            string   `json:"id"`
				Title         string   `json:"title"`
				ExternalPath  string   `json:"externalPath"`
				LocationsText string   `json:"locationsText"`
				BulletFields  []string `json:"bulletFields"`
				PostedOn      string   `json:"postedOn"`
			} `json:"jobPostings"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		if len(res.JobPostings) == 0 {
			if res.Total > 0 && offset < res.Total {
				fmt.Printf("workday: pagination cap hit for %q at offset=%d total=%d; results truncated\n", searchText, offset, res.Total)
			}
			break
		}

		base := endpoint
		if i := strings.Index(base, "/wday/cxs/"); i >= 0 {
			base = base[:i]
		}
		for _, j := range res.JobPostings {
			location := strings.TrimSpace(j.LocationsText)
			if location == "" && len(j.BulletFields) > 0 {
				location = strings.TrimSpace(j.BulletFields[0])
			}
			postedAt, _ := time.Parse("2006-01-02", j.PostedOn)
			jobURL := j.ExternalPath
			if strings.HasPrefix(jobURL, "/") {
				jobURL = base + jobURL
			}
			postings = append(postings, Posting{
				ExternalID: j.ID,
				Title:      j.Title,
				URL:        jobURL,
				Location:   location,
				PostedAt:   postedAt,
			})
		}

		offset += limit
		if res.Total > 0 && offset >= res.Total {
			break
		}
	}

	return postings, nil
}

func workdayFetchListingsPlaywright(careersURL string) ([]Posting, error) {
	s := strings.TrimSpace(careersURL)
	if s == "" {
		return nil, fmt.Errorf("workday listing scrape: empty url")
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("workday listing playwright run: %w", err)
	}
	defer func() { _ = pw.Stop() }()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("workday listing chromium: %w", err)
	}
	defer func() { _ = browser.Close() }()

	page, err := browser.NewPage()
	if err != nil {
		return nil, err
	}

	if _, err := page.Goto(s, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(90000),
	}); err != nil {
		if _, err2 := page.Goto(s, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(90000),
		}); err2 != nil {
			return nil, fmt.Errorf("workday listing goto: %w", err)
		}
	}
	page.WaitForTimeout(5000)

	rawRows, err := page.Evaluate(`() => {
		const rows = [];
		const seen = new Set();
		const anchors = Array.from(document.querySelectorAll('a[href*="/job/"]'));

		for (const a of anchors) {
			const rawHref = a.getAttribute('href') || '';
			if (!rawHref || seen.has(rawHref)) continue;
			const title = (a.textContent || '').replace(/\s+/g, ' ').trim();
			if (!title || title.length < 2) continue;
			seen.add(rawHref);

			const href = rawHref.startsWith('http') ? rawHref : new URL(rawHref, window.location.href).href;

			let externalID = '';
			const r = href.match(/_R[0-9]+/);
			if (r) externalID = r[0].substring(1);
			if (!externalID) {
				const parts = href.split('/').filter(Boolean);
				externalID = parts.length ? parts[parts.length - 1].split('?')[0] : href;
			}

			let location = '';
			const card = a.closest('[data-automation-id="jobPosting"], li, [role="listitem"]');
			if (card) {
				const locEl = card.querySelector('[data-automation-id="locations"], [data-automation-id="location"]');
				if (locEl) location = (locEl.textContent || '').replace(/\s+/g, ' ').trim();
			}

			rows.push({ externalID, title, url: href, location });
		}
		return rows;
	}`)
	if err != nil {
		return nil, fmt.Errorf("workday listing evaluate: %w", err)
	}

	var rows []struct {
		ExternalID string `json:"externalID"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		Location   string `json:"location"`
	}
	rawJSON, err := json.Marshal(rawRows)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawJSON, &rows); err != nil {
		return nil, err
	}

	postings := make([]Posting, 0, len(rows))
	for _, row := range rows {
		postings = append(postings, Posting{
			ExternalID: row.ExternalID,
			Title:      row.Title,
			URL:        row.URL,
			Location:   row.Location,
			PostedAt:   time.Time{},
		})
	}
	if len(postings) == 0 {
		return nil, fmt.Errorf("workday: no job links found on listing page (try scrolling or use CXS /jobs URL from DevTools)")
	}
	return postings, nil
}

func resolveWorkdayJobsEndpoint(client *http.Client, slug string) (string, error) {
	s := strings.TrimSpace(slug)
	if s == "" {
		return "", fmt.Errorf("workday scraper requires non-empty slug")
	}
	if strings.Contains(s, "/wday/cxs/") && strings.HasSuffix(s, "/jobs") {
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			return s, nil
		}
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		req, err := http.NewRequest(http.MethodGet, s, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("unexpected status %d while resolving workday endpoint", resp.StatusCode)
		}
		html := string(b)
		if ep := findWorkdayCXSJobsURL(html, s); ep != "" {
			return ep, nil
		}
		return "", fmt.Errorf("could not find workday /wday/cxs/.../jobs endpoint in page")
	}
	return "", fmt.Errorf("workday slug must be a full careers URL or /wday/cxs/.../jobs endpoint")
}

func findWorkdayCXSJobsURL(html, pageURL string) string {
	reAbs := regexp.MustCompile(`https://[^"'\\s<>]+/wday/cxs/[^"'\\s<>]+/jobs`)
	if m := reAbs.FindString(html); m != "" {
		return strings.TrimSuffix(m, "\\")
	}
	reRel := regexp.MustCompile(`/wday/cxs/[^"'\\s<>]+/jobs`)
	if m := reRel.FindString(html); m != "" {
		pu, err := url.Parse(pageURL)
		if err != nil || pu.Scheme == "" || pu.Host == "" {
			return ""
		}
		return pu.Scheme + "://" + pu.Host + m
	}
	return ""
}

func resolveWorkdayJobsEndpointPlaywright(careersURL string) (string, error) {
	s := strings.TrimSpace(careersURL)
	if s == "" {
		return "", fmt.Errorf("workday playwright: empty url")
	}
	if strings.Contains(s, "/wday/cxs/") && strings.HasSuffix(s, "/jobs") {
		return s, nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return "", fmt.Errorf("workday playwright run: %w", err)
	}
	defer func() { _ = pw.Stop() }()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("workday chromium: %w", err)
	}
	defer func() { _ = browser.Close() }()

	page, err := browser.NewPage()
	if err != nil {
		return "", err
	}

	if _, err := page.Goto(s, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(90000),
	}); err != nil {
		if _, err2 := page.Goto(s, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(90000),
		}); err2 != nil {
			return "", fmt.Errorf("workday page goto: %w", err)
		}
	}
	page.WaitForTimeout(5000)

	html, err := page.Content()
	if err != nil {
		return "", err
	}
	if ep := findWorkdayCXSJobsURL(html, s); ep != "" {
		return ep, nil
	}
	return "", fmt.Errorf("workday: could not discover /wday/cxs/.../jobs after loading page (tenant may use non-standard jobs UI)")
}

type AppleScraper struct{}

func (a *AppleScraper) Fetch(slug string) ([]Posting, error) {
	team := strings.TrimSpace(slug)
	if team == "" {
		return nil, fmt.Errorf("apple scraper requires non-empty team slug")
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("apple playwright run failed: %w", err)
	}
	defer func() {
		_ = pw.Stop()
	}()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("apple chromium launch failed: %w", err)
	}
	defer func() {
		_ = browser.Close()
	}()

	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("apple page create failed: %w", err)
	}

	url := fmt.Sprintf("https://jobs.apple.com/en-us/search?team=%s", team)
	if _, err := page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(60000),
	}); err != nil {
		return nil, fmt.Errorf("apple page goto failed: %w", err)
	}

	page.WaitForTimeout(4000)

	rawRows, err := page.Evaluate(`() => {
		const cards = Array.from(document.querySelectorAll('#search-job-list > li, li.rc-accordion-item'));
		const rows = [];
		const dateRegex = /\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2},\s+\d{4}\b/;

		for (const card of cards) {
			const a = card.querySelector('a[href*="/en-us/details/"]');
			if (!a) continue;
			const href = a.href || '';
			if (!href) continue;
			const title = (a.textContent || '').trim();
			if (!title) continue;

			const idMatch = href.match(/\/details\/([^\/?#]+)/);
			const externalID = idMatch ? idMatch[1] : href;
			const blockText = (card.innerText || '')
				.split('\n')
				.map(s => s.trim())
				.filter(Boolean);

			let postedAtRaw = '';
			let location = '';
			for (const line of blockText) {
				if (!postedAtRaw && dateRegex.test(line)) {
					const m = line.match(dateRegex);
					postedAtRaw = m ? m[0] : '';
				}
			}

			// Apple jobs markup: #search-location-search-job-title-* contains
			// an a11y "Location" span and a sibling span with the city name.
			const locationBlock = card.querySelector('[id^="search-location-search-job-title-"]');
			if (locationBlock) {
				const spans = Array.from(locationBlock.querySelectorAll('span'));
				for (const span of spans) {
					const txt = (span.textContent || '').trim();
					if (!txt) continue;
					if (txt.toLowerCase() === 'location') continue;
					location = txt;
					break;
				}
			}
			if (!location) {
				const explicit = card.querySelector('[id^="search-store-name-container-"]');
				if (explicit) location = (explicit.textContent || '').trim();
			}

			rows.push({ externalID, title, url: href, location, postedAtRaw });
		}
		return rows;
	}`)
	if err != nil {
		return nil, fmt.Errorf("apple evaluate failed: %w", err)
	}

	var rows []struct {
		ExternalID string `json:"externalID"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		Location   string `json:"location"`
		PostedAt   string `json:"postedAtRaw"`
	}
	rawJSON, err := json.Marshal(rawRows)
	if err != nil {
		return nil, fmt.Errorf("apple playwright marshal failed: %w", err)
	}
	if err := json.Unmarshal(rawJSON, &rows); err != nil {
		return nil, fmt.Errorf("apple playwright parse failed: %w", err)
	}

	postings := make([]Posting, 0, len(rows))
	for _, row := range rows {
		postedAt, _ := time.Parse("Jan 2, 2006", row.PostedAt)
		postings = append(postings, Posting{
			ExternalID: row.ExternalID,
			Title:      row.Title,
			URL:        row.URL,
			Location:   row.Location,
			PostedAt:   postedAt,
		})
	}

	return postings, nil
}

type GoogleScraper struct{}

func (g *GoogleScraper) Fetch(slug string) ([]Posting, error) {
	searchURL := strings.TrimSpace(slug)
	if searchURL == "" {
		return nil, fmt.Errorf("google scraper requires search URL slug")
	}
	if !strings.HasPrefix(searchURL, "http://") && !strings.HasPrefix(searchURL, "https://") {
		searchURL = "https://www.google.com/about/careers/applications/jobs/results?" + strings.TrimPrefix(searchURL, "?")
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("google playwright run failed: %w", err)
	}
	defer func() {
		_ = pw.Stop()
	}()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("google chromium launch failed: %w", err)
	}
	defer func() {
		_ = browser.Close()
	}()

	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("google page create failed: %w", err)
	}

	if _, err := page.Goto(searchURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(60000),
	}); err != nil {
		return nil, fmt.Errorf("google page goto failed: %w", err)
	}
	page.WaitForTimeout(4000)

	rawRows, err := page.Evaluate(`() => {
		const rows = [];
		const seen = new Set();
		const dateRegex = /\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2},\s+\d{4}\b/;
		const anchors = Array.from(document.querySelectorAll('a[href*="jobs/results/"]'));

		for (const a of anchors) {
			const rawHref = a.getAttribute('href') || '';
			const href = rawHref ? new URL(rawHref, window.location.href).href : '';
			if (!href || seen.has(href)) continue;
			seen.add(href);

			const card = a.closest('li, article, div') || a.parentElement;
			const blockText = (card ? card.innerText : '')
				.split('\n')
				.map(s => s.trim())
				.filter(Boolean);

			let title = (a.textContent || '').trim();
			if (!title && blockText.length > 0) title = blockText[0];
			if (!title) continue;

			let location = '';
			let postedAtRaw = '';
			for (const line of blockText) {
				if (!postedAtRaw && dateRegex.test(line)) {
					const m = line.match(dateRegex);
					postedAtRaw = m ? m[0] : '';
				}
				if (!location && line.startsWith('Google |')) {
					location = line.replace(/^Google \|\s*/, '').trim();
				}
			}
			if (!location) {
				for (const line of blockText) {
					if (line === title) continue;
					if (line.includes('Minimum qualifications')) continue;
					if (line.includes('Learn more')) continue;
					if (line.includes('Copy link')) continue;
					if (line.includes('Email a friend')) continue;
					if (line.length > 120) continue;
					if (line.includes(',') || line.includes(';')) {
						location = line;
						break;
					}
				}
			}

			const idMatch = href.match(/\/results\/([^/?#]+)/);
			const externalID = idMatch ? idMatch[1] : href;
			rows.push({ externalID, title, url: href, location, postedAtRaw });
		}
		return rows;
	}`)
	if err != nil {
		return nil, fmt.Errorf("google evaluate failed: %w", err)
	}

	var rows []struct {
		ExternalID string `json:"externalID"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		Location   string `json:"location"`
		PostedAt   string `json:"postedAtRaw"`
	}
	rawJSON, err := json.Marshal(rawRows)
	if err != nil {
		return nil, fmt.Errorf("google playwright marshal failed: %w", err)
	}
	if err := json.Unmarshal(rawJSON, &rows); err != nil {
		return nil, fmt.Errorf("google playwright parse failed: %w", err)
	}

	postings := make([]Posting, 0, len(rows))
	for _, row := range rows {
		postedAt, _ := time.Parse("Jan 2, 2006", row.PostedAt)
		postings = append(postings, Posting{
			ExternalID: row.ExternalID,
			Title:      row.Title,
			URL:        row.URL,
			Location:   row.Location,
			PostedAt:   postedAt,
		})
	}
	return postings, nil
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

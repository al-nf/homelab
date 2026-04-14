# job-monitor

Hourly job posting monitor with Discord notifications.

## Structure

```
cmd/
  scraper/   - hourly scrape loop (run via systemd timer)
  bot/       - optional interactive Discord bot
internal/
  db/        - SQLite layer (companies, postings)
  scraper/   - per-ATS scrapers (Greenhouse, Lever, Workday, generic)
  notifier/  - Discord webhook notifications
```

## ATS support

| ATS        | Method         | Difficulty |
|------------|----------------|------------|
| Greenhouse | Public JSON API | easy      |
| Lever      | Public JSON API | easy      |
| Workday    | HTML/XHR        | hard      |
| Generic    | CSS selector    | varies    |

Start with Greenhouse and Lever — they cover a large chunk of tech companies
and require no scraping, just JSON parsing.

## Setup

```
cp .env.example /etc/job-monitor/env
go build ./cmd/scraper -o /usr/local/bin/job-monitor-scraper
cp job-monitor.{service,timer} /etc/systemd/system/
systemctl enable --now job-monitor.timer
```

## Bot commands

```
!new [n]         show postings found in last n hours (default 24)
!status <slug>   last checked time + posting count
!add <ats> <url> add company to DB
!list            list all tracked companies
```

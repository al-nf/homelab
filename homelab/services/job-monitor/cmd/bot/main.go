package main

// Entry point for the optional interactive Discord bot.
// Provides commands to query the DB without leaving Discord.
//
// Commands:
//   !new [n]         - show postings added in the last n hours (default 24)
//   !status <slug>   - show last_checked and posting count for a company
//   !add <ats> <url> - add a new company to the database
//   !list            - list all tracked companies

func main() {
	// TODO: load config (bot token, DB path)
	// TODO: open DB
	// TODO: register command handlers
	// TODO: start bot and block
}

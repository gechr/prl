package main

import (
	"strings"

	"github.com/gechr/clog"
)

func discoverBotAuthors(cfg *Config) map[string]bool {
	bots := make(map[string]bool)

	plug, err := discoverPlugin(cfg)
	if err != nil {
		clog.Debug().Err(err).Msg("Skipping plugin bot resolution")
		return bots
	}

	pluginBots, err := plug.ResolveBots()
	if err != nil {
		clog.Debug().Err(err).Msg("Skipping plugin bot resolution")
		return bots
	}

	for _, username := range pluginBots {
		bots[strings.ToLower(trimBotSuffix(username))] = true
	}

	return bots
}

func normalizeBotAuthorValue(username string, bots map[string]bool) string {
	if strings.HasSuffix(strings.ToLower(username), BotSuffix) {
		return username
	}
	if bots[strings.ToLower(username)] {
		return username + BotSuffix
	}
	return username
}

func trimBotSuffix(username string) string {
	if trimmed, ok := strings.CutSuffix(username, BotSuffix); ok {
		return trimmed
	}
	return username
}

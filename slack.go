package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// renderSlack formats PRs for Slack posting and computes reactions.
// Filters out skip repos, splits into one-approver and two-approver groups.
//
// Reaction rules:
//   - Single PR: :two: if two-approver repo, :automerged: if auto-merge enabled.
//   - Multiple PRs: :automerged: if any PR has auto-merge (two-approver is in the message body).
func renderSlack(prs []PullRequest, cfg *Config) (string, []string) {
	skipRepos := toSet(cfg.Output.Slack.SkipRepos)
	twoApproverRepos := toSet(cfg.Output.Slack.TwoApproverRepos)

	var oneApprover, twoApprover []string
	hasAutomerge := false

	for _, pr := range prs {
		repoFullName := strings.ToLower(pr.Repository.NameWithOwner)
		if skipRepos[repoFullName] {
			continue
		}
		if pr.Automerge {
			hasAutomerge = true
		}
		if twoApproverRepos[repoFullName] {
			twoApprover = append(twoApprover, pr.URL)
		} else {
			oneApprover = append(oneApprover, pr.URL)
		}
	}

	natsort(oneApprover)
	natsort(twoApprover)

	total := len(oneApprover) + len(twoApprover)

	// Compute reactions.
	var reactions []string
	if total == 1 {
		// Single PR: :two: first, then :automerge:.
		if len(twoApprover) > 0 {
			reactions = append(reactions, ":two:")
		}
		if hasAutomerge {
			reactions = append(reactions, ":automerged:")
		}
	} else if hasAutomerge {
		// Multiple PRs: :two: is in the message body; only :automerged: as reaction.
		reactions = append(reactions, ":automerged:")
	}

	// Single PR: inline emoji on the same line.
	if total == 1 {
		url := oneApprover
		if len(twoApprover) > 0 {
			url = twoApprover
		}
		return url[0] + " 🙏", reactions
	}

	var sb strings.Builder

	if len(twoApprover) > 0 {
		// Show with emoji headers
		if len(oneApprover) > 0 {
			sb.WriteString("1️⃣\n")
			for _, url := range oneApprover {
				sb.WriteString(url + "\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("2️⃣\n")
		for _, url := range twoApprover {
			sb.WriteString(url + "\n")
		}
	} else {
		// Just URLs
		for _, url := range oneApprover {
			sb.WriteString(url + "\n")
		}
	}

	// Always append 🙏 emoji at the end
	result := strings.TrimRight(sb.String(), "\n")
	return result + "\n\n🙏", reactions
}

// renderSlackDisplay renders the per-recipient groups as they will actually be
// sent, separated by a blank line. Used for stdout preview when --send is active.
func renderSlackDisplay(prs []PullRequest, cfg *Config) string {
	groups := groupBySlackRecipient(prs, cfg)
	if len(groups) == 0 {
		msg, _ := renderSlack(prs, cfg)
		return msg
	}
	recipients := make([]string, 0, len(groups))
	for r := range groups {
		recipients = append(recipients, r)
	}
	natsort(recipients)
	var parts []string
	for _, r := range recipients {
		msg, _ := renderSlack(groups[r], cfg)
		if msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, "\n\n")
}

// sendToSlack sends the given message to the specified Slack recipient via the
// slack CLI. recipient may be a #channel, @user, or email address.
// When sendAt is non-empty it is passed as --at to schedule the message.
// Reactions are added via --react flags.
func sendToSlack(message, recipient, sendAt string, reactions []string) (string, error) {
	args := []string{"send"}
	if sendAt != "" {
		args = append(args, "--at", sendAt)
	}
	for _, r := range reactions {
		args = append(args, "--react", r)
	}
	args = append(args, recipient)

	cmd := exec.CommandContext(context.Background(), "slack", args...)
	cmd.Stdin = strings.NewReader(message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return "", fmt.Errorf("slack send: %w: %s", err, detail)
		}
		return "", fmt.Errorf("slack send: %w", err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return first, nil
}

// normalizeSlackChannel ensures a Slack recipient has a leading "#" when it
// doesn't already start with "#" (channel) or "@" (user mention).
func normalizeSlackChannel(recipient string) string {
	if recipient == "" || strings.HasPrefix(recipient, "#") || strings.HasPrefix(recipient, "@") {
		return recipient
	}
	return "#" + recipient
}

// slackRecipientForRepo returns the configured Slack recipient for the given
// repo (in <org>/<repo> format). It checks each channel's repo list for an
// exact match first, then falls back to whichever channel has "*". Returns ""
// if no recipient is configured.
func slackRecipientForRepo(repo string, recipients slackRecipients) string {
	lower := strings.ToLower(repo)
	defaultChannel := ""
	for channel, repos := range recipients {
		for _, r := range repos {
			if strings.ToLower(r) == lower {
				return channel
			}
			if r == "*" {
				defaultChannel = channel
			}
		}
	}
	return defaultChannel
}

// sendSlack renders and sends PRs to Slack. If cli.SendTo is set it overrides
// all routing and sends everything to that single recipient; otherwise PRs are
// grouped by the recipients config and sent per recipient.
// Automerge status is enriched on-demand for accurate reactions.
func sendSlack(cli *CLI, cfg *Config, prs []PullRequest) (string, error) {
	// Enrich automerge status so renderSlack can compute reactions.
	if gql, err := newGraphQLClient(withDebug(cli.Debug)); err == nil {
		_ = enrichAutomerge(gql, prs)
	}

	if cli.SendTo != "" {
		msg, reactions := renderSlack(prs, cfg)
		if msg == "" {
			return "", nil
		}
		return sendToSlack(msg, cli.SendTo, cli.SendAt, reactions)
	}
	groups := groupBySlackRecipient(prs, cfg)
	if len(groups) == 0 {
		return "", fmt.Errorf(
			"--send: no Slack recipients configured (set output.slack.recipients in config or use --send-to)",
		)
	}
	var lastOutput string
	for recipient, recipientPRs := range groups {
		msg, reactions := renderSlack(recipientPRs, cfg)
		if msg == "" {
			continue
		}
		out, err := sendToSlack(msg, recipient, cli.SendAt, reactions)
		if err != nil {
			return "", fmt.Errorf("sending to Slack recipient %s: %w", recipient, err)
		}
		lastOutput = out
	}
	return lastOutput, nil
}

// groupBySlackRecipient groups PRs by their destination Slack recipient, skipping
// any repos in cfg.Output.Slack.SkipRepos and any PRs with no configured recipient.
func groupBySlackRecipient(prs []PullRequest, cfg *Config) map[string][]PullRequest {
	skipRepos := toSet(cfg.Output.Slack.SkipRepos)
	groups := make(map[string][]PullRequest)
	for _, pr := range prs {
		repo := strings.ToLower(pr.Repository.NameWithOwner)
		if skipRepos[repo] {
			continue
		}
		r := slackRecipientForRepo(pr.Repository.NameWithOwner, cfg.Output.Slack.Recipients)
		if r == "" {
			continue
		}
		groups[r] = append(groups[r], pr)
	}
	return groups
}

// toSet converts a string slice to a lowercase set.
func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[strings.ToLower(item)] = true
	}
	return m
}

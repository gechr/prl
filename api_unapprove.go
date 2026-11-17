package main

import (
	"fmt"
	"strings"
)

// unapprovePR dismisses only approving reviews authored by the authenticated user.
func (a *ActionRunner) unapprovePR(pr PullRequest, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("an approval dismissal message is required")
	}
	login, err := a.cachedCurrentLogin()
	if err != nil {
		return err
	}
	if login == "" {
		return fmt.Errorf("cannot remove approval without the current user")
	}
	owner, repo := prOwnerRepo(pr)
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, pr.Number)
	var ids []int64
	for page := 1; ; page++ {
		var reviews []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := a.rest.Get(
			fmt.Sprintf("%s?per_page=%d&page=%d", path, maxPerPage, page),
			&reviews,
		); err != nil {
			return err
		}
		for _, review := range reviews {
			if strings.EqualFold(review.User.Login, login) && review.State == valueReviewApproved {
				ids = append(ids, review.ID)
			}
		}
		if len(reviews) < maxPerPage {
			break
		}
	}
	for _, id := range ids {
		body := jsonBody(map[string]string{"message": message})
		if err := a.rest.Put(fmt.Sprintf("%s/%d/dismissals", path, id), body, nil); err != nil {
			return err
		}
	}
	return nil
}

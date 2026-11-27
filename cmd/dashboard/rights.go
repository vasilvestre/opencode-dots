package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// What a visitor's own GitHub token says they may do (ADR 0046). visibleRepos runs once, inside
// the sign-in poll, with the token as an argument: nothing here keeps it, and no error or log
// line carries it. A repository GitHub does not return, a 403, a 404 or any failure is hidden.

const (
	graphqlChunk = 50
	permParallel = 8
	rankPull     = 1 // read
	rankTriage   = 2
	rankPush     = 3 // write
	rankMaintain = 4
	rankAdmin    = 5
)

// parseMinPermission turns the setting into the rank a visitor needs. Empty is push.
func parseMinPermission(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "push":
		return rankPush, nil
	case "pull":
		return rankPull, nil
	}
	return 0, fmt.Errorf("min permission %q is not valid: use push or pull", s)
}

func minPermissionName(rank int) string {
	if rank == rankPull {
		return "pull"
	}
	return "push"
}

// graphqlRank maps GraphQL's viewerPermission; an unknown value is 0, which nothing admits.
func graphqlRank(p string) int {
	switch p {
	case "READ":
		return rankPull
	case "TRIAGE":
		return rankTriage
	case "WRITE":
		return rankPush
	case "MAINTAIN":
		return rankMaintain
	case "ADMIN":
		return rankAdmin
	}
	return 0
}

// restRank maps the permissions object of GET /repos/{owner}/{repo}.
func restRank(p map[string]bool) int {
	switch {
	case p["admin"]:
		return rankAdmin
	case p["maintain"]:
		return rankMaintain
	case p["push"]:
		return rankPush
	case p["triage"]:
		return rankTriage
	case p["pull"]:
		return rankPull
	}
	return 0
}

// visibleRepos is the subset of repos (lower-cased) where the token's owner reaches the minimum.
func (a *authState) visibleRepos(token string, repos []string) map[string]bool {
	var valid []string
	for _, r := range repos {
		if repoNameRE.MatchString(r) {
			valid = append(valid, r)
		}
	}
	ranks, ok := a.graphqlRanks(token, valid)
	if !ok {
		ranks = a.restRanks(token, valid)
	}
	out := map[string]bool{}
	for repo, rank := range ranks {
		if rank >= a.minRank {
			out[strings.ToLower(repo)] = true
		}
	}
	return out
}

func (a *authState) apiRequest(method, path, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, a.apiURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return a.client.Do(req)
}

// graphqlRanks asks for every repository's viewerPermission, 50 to a call. ok is false when
// GitHub's GraphQL did not answer usably, so the caller falls back to REST.
func (a *authState) graphqlRanks(token string, repos []string) (map[string]int, bool) {
	ranks := map[string]int{}
	for start := 0; start < len(repos); start += graphqlChunk {
		chunk := repos[start:min(start+graphqlChunk, len(repos))]
		var q strings.Builder
		q.WriteString("query{")
		for i, r := range chunk {
			owner, name, _ := strings.Cut(r, "/")
			fmt.Fprintf(&q, "r%d:repository(owner:%q,name:%q){viewerPermission}", i, owner, name)
		}
		q.WriteString("}")
		payload, _ := json.Marshal(map[string]string{"query": q.String()})
		resp, err := a.apiRequest(http.MethodPost, "/graphql", token, bytes.NewReader(payload))
		if err != nil {
			return nil, false
		}
		var out struct {
			Data map[string]*struct {
				ViewerPermission string `json:"viewerPermission"`
			} `json:"data"`
		}
		bad := resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, githubBodyCap)).Decode(&out) != nil || out.Data == nil
		resp.Body.Close()
		if bad {
			return nil, false
		}
		for i, r := range chunk {
			if e := out.Data[fmt.Sprintf("r%d", i)]; e != nil { // null: unknown or unreadable, hidden
				ranks[r] = graphqlRank(e.ViewerPermission)
			}
		}
	}
	return ranks, true
}

// restRanks is the fallback: GET /repos/{owner}/{repo} for each repository.
func (a *authState) restRanks(token string, repos []string) map[string]int {
	ranks := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, permParallel)
	for _, repo := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if rank := a.restRank(token, repo); rank > 0 {
				mu.Lock()
				ranks[repo] = rank
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return ranks
}

func (a *authState) restRank(token, repo string) int {
	owner, name, _ := strings.Cut(repo, "/")
	resp, err := a.apiRequest(http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), token, nil)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var out struct {
		Permissions map[string]bool `json:"permissions"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, githubBodyCap)).Decode(&out) != nil {
		return 0
	}
	return restRank(out.Permissions)
}

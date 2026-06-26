package solver

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	salamoonder "github.com/salamoonder-llc/salamoonder-go"
)

// salamoonderResolver solves Datadome through the Salamoonder service.
//
// It fetches the challenge through Salamoonder's own egress (and the SAME proxy
// used for API calls, when set) so the minted Datadome cid is bound to that IP;
// minting the cid on our own IP and handing it to Salamoonder's FR pool makes
// DataDome reject the solution ("DD_..." support error, no cookie).
type salamoonderResolver struct {
	apiKey    string
	userAgent string
	proxy     string
}

func newSalamoonderResolver(cfg resolverConfig) (Resolver, error) {
	apiKey := strings.TrimSpace(os.Getenv("SALAMOONDER_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("solver: SALAMOONDER_API_KEY is not set")
	}
	return &salamoonderResolver{
		apiKey:    apiKey,
		userAgent: cfg.userAgent,
		proxy:     cfg.proxy,
	}, nil
}

func (r *salamoonderResolver) Name() string { return "salamoonder" }

func (r *salamoonderResolver) Solve(challengeURL string) (string, error) {
	client, err := salamoonder.New(r.apiKey)
	if err != nil {
		return "", fmt.Errorf("solver: %w", err)
	}

	hdr := map[string]string{
		"User-Agent": r.userAgent,
		"Accept":     "application/json, text/plain, */*",
	}
	// Fetch the challenge through Salamoonder (and the SAME proxy used for API
	// calls, when set) so the minted Datadome cid is bound to that egress IP.
	resp, err := client.Get(challengeURL, &salamoonder.RequestOptions{Headers: hdr, Proxy: r.proxy})
	if err != nil {
		return "", fmt.Errorf("solver: fetch challenge: %w", err)
	}

	var sliderURL string
	var body struct {
		URL string `json:"url"`
	}
	switch {
	case json.Unmarshal([]byte(resp.Text), &body) == nil && strings.Contains(body.URL, "captcha-delivery.com"):
		// API JSON 403 — the captcha URL is handed to us directly, but in a
		// shape Salamoonder's slider solver rejects (carries `b`, lacks
		// `dm=cd`). Normalise it to the canonical slider form.
		sliderURL, err = canonicalSliderURL(body.URL)
		if err != nil {
			return "", fmt.Errorf("solver: %w", err)
		}
	case strings.Contains(resp.Text, "var dd="):
		// Classic HTML challenge page.
		sliderURL, err = client.Datadome.ParseSliderURL(resp.Text, resp.Cookies.Get("datadome"), challengeURL)
		if err != nil {
			return "", fmt.Errorf("solver: parse slider URL: %w", err)
		}
	default:
		return "", fmt.Errorf("solver: no challenge present (status %d) — nothing to solve", resp.StatusCode)
	}

	taskID, err := client.Task.CreateTask("DataDomeSliderSolver", map[string]interface{}{
		"captcha_url":  sliderURL,
		"user_agent":   r.userAgent,
		"country_code": "fr",
	})
	if err != nil {
		return "", fmt.Errorf("solver: create task: %w", err)
	}

	sol, err := client.Task.GetTaskResult(taskID, 2)
	if err != nil {
		return "", fmt.Errorf("solver: solve: %w", err)
	}
	solMap, _ := sol.(map[string]interface{})
	if provErr, _ := solMap["error"].(string); provErr != "" {
		return "", fmt.Errorf("solver: Salamoonder could not solve intermarche's Datadome challenge: %s — this is a provider-side limitation (the captcha URL and IP binding are correct); contact Salamoonder support with that code or switch solving provider (CAPTCHA_PROVIDER=2captcha)", provErr)
	}
	cookieStr, _ := solMap["cookie"].(string)
	if cookieStr == "" {
		return "", fmt.Errorf("solver: solution had no cookie: %v", sol)
	}
	return stripDatadome(cookieStr), nil
}

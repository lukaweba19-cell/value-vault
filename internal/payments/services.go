package payments

import (
	"encoding/json"
	"strings"
	"time"
)

func DetectActiveServices(txStr string) string {
	if strings.TrimSpace(txStr) == "" {
		return "No subscriptions"
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(txStr), &root); err != nil {
		return "No subscriptions"
	}

	list, _ := root["subscriptions"].([]interface{})
	if len(list) == 0 {
		return "No subscriptions"
	}

	now := time.Now().UTC()

	// title -> keep the "best" currently-valid instance
	type subInfo struct {
		title     string
		state     string
		autoRenew bool
		until     time.Time
	}
	active := make(map[string]subInfo)

	parseDate := func(s string) (time.Time, bool) {
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, false
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.9999999Z07:00",
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}

	isLiveState := func(state string) bool {
		s := strings.ToLower(strings.TrimSpace(state))
		switch s {
		case "active", "enabled", "activating", "pending", "trial", "trialing":
			return true
		default:
			return false
		}
	}

	isDeadState := func(state string) bool {
		s := strings.ToLower(strings.TrimSpace(state))
		switch s {
		case "failed", "inactive", "expired", "revoked", "terminated":
			return true
		default:
			return false
		}
	}

	for _, item := range list {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		title, _ := obj["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		state, _ := obj["recurrenceState"].(string)
		autoRenew, _ := obj["autoRenew"].(bool)

		untilStr, _ := obj["nextRenewalDate"].(string)
		until, hasUntil := parseDate(untilStr)

		// Currently entitled if:
		// 1) state is live, or
		// 2) nextRenewalDate is still in the future (paid period not over yet),
		//    even when canceled / autoRenew=false.
		// Dead states with a past (or missing) end date are ignored.
		entitled := false
		if isLiveState(state) {
			entitled = true
		} else if hasUntil && until.After(now) && !isDeadState(state) {
			// Canceled but still within the paid window
			entitled = true
		} else if hasUntil && until.After(now) && strings.EqualFold(state, "Canceled") {
			entitled = true
		}

		if !entitled {
			continue
		}

		info := subInfo{title: title, state: state, autoRenew: autoRenew, until: until}
		if prev, exists := active[title]; !exists || info.until.After(prev.until) {
			active[title] = info
		}
	}

	if len(active) == 0 {
		return "No subscriptions"
	}

	// Prefer the well-known Game Pass / Gold labels in the old "Services: X" style.
	// If several are valid at once, pick the highest tier; otherwise list others.
	priority := []string{
		"Xbox Game Pass Ultimate",
		"PC Game Pass",
		"Xbox Game Pass Core",
		"Xbox Game Pass Essential",
		"Xbox Game Pass",
		"Xbox Live Gold",
		"Xbox Live Gold / Game Pass Core",
	}

	pick := ""
	for _, name := range priority {
		if _, ok := active[name]; ok {
			pick = name
			break
		}
		// fuzzy: title contains the known name
		if pick == "" {
			for t := range active {
				if strings.EqualFold(t, name) || strings.Contains(strings.ToLower(t), strings.ToLower(name)) {
					pick = t
					break
				}
			}
		}
		if pick != "" {
			break
		}
	}

	if pick != "" {
		return pick
	}

	// Fallback: join remaining titles (Fortnite Crew, Realms, etc.)
	var titles []string
	for t := range active {
		titles = append(titles, t)
	}
	return strings.Join(titles, ", ")
}

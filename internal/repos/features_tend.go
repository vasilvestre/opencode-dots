package repos

import (
	"strconv"
	"strings"
)

// tendLine renders one tend.list line: `repo auto_merge`, then optional
// key=value fields. A reader that only knows the first two columns ignores the
// rest, and a repository with neither field keeps the two column line.
func tendLine(t TendEntry) string {
	line := t.Repo + " " + strconv.FormatBool(t.AutoMerge)
	if t.Autonomy != "" {
		line += " autonomy=" + t.Autonomy
	}
	if t.Upstream != "" {
		line += " upstream=" + t.Upstream
	}
	if t.Identity != "" {
		line += " identity=" + t.Identity
	}
	return line
}

// tendExtras gives the ceiling, the upstream list and the identity mode a daily
// pass receives. Identity is only the explicit key, like autonomy.
// Autonomy is only the explicit key: the value EffectiveAutonomy derives from
// tend.auto_merge is not a ceiling the repository asked for, so it stays empty.
// Upstream is the string the rules get: `owner/name=cap` joined by commas.
func tendExtras(r *Resolved) (autonomy, upstream, identity string) {
	autonomy, _ = r.Values["autonomy"].(string)
	identity, _ = r.Values["identity"].(string)
	if raw, ok := r.Values["upstream"]; ok {
		if entries, err := upstreamEntries(raw); err == nil {
			parts := make([]string, len(entries))
			for i, e := range entries {
				parts[i] = e[0] + "=" + e[1]
			}
			upstream = strings.Join(parts, ",")
		}
	}
	return autonomy, upstream, identity
}

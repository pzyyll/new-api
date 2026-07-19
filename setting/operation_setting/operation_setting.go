package operation_setting

import "strings"

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

func AutomaticDisableKeywordsToString() string {
	return strings.Join(AutomaticDisableKeywords, "\n")
}

func AutomaticDisableKeywordsFromString(s string) {
	AutomaticDisableKeywords = []string{}
	ak := strings.Split(s, "\n")
	for _, k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			AutomaticDisableKeywords = append(AutomaticDisableKeywords, k)
		}
	}
}

// UpstreamCapacityKeywords are case-insensitive substrings used to classify
// transient capacity/overload soft failures (retryable, no auto-ban).
// Defaults cover common provider messages such as xAI high-demand capacity.
var UpstreamCapacityKeywords = []string{
	"at capacity",
	"high demand",
	"overloaded",
	"temporarily unavailable",
	"server is busy",
	"priority processing",
}

func UpstreamCapacityKeywordsToString() string {
	return strings.Join(UpstreamCapacityKeywords, "\n")
}

func UpstreamCapacityKeywordsFromString(s string) {
	UpstreamCapacityKeywords = []string{}
	for _, line := range strings.Split(s, "\n") {
		k := strings.ToLower(strings.TrimSpace(line))
		if k != "" {
			UpstreamCapacityKeywords = append(UpstreamCapacityKeywords, k)
		}
	}
}

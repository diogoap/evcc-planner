package calendar

import (
	"strings"
	"unicode"

	"github.com/diogoap/evcc-planner/internal/model"
)

// Reasons an event is not a car trip.
const (
	ReasonOnline = "Online meeting"
	ReasonAtHome = "At home"
)

// Classification says whether a calendar event is a car trip.
type Classification struct {
	Trip        bool
	NeedsReview bool   // counted toward demand, but car use is unclear
	Reason      string // why it is not a trip
}

var onlineMarkers = []string{
	"http://", "https://", "meet.google.", "zoom.us", "teams.microsoft.", "teams.live.", "webex",
	"microsoft teams", "google meet", "skype", "online",
}

// Classify applies the trip rules: online meetings and events at home are
// not trips. An event without a location is a trip that stays disabled until
// it gets one (it has no route). Saved places and address-like locations are
// clear; anything else is counted but marked for review.
func Classify(location, home string, places []model.Place) Classification {
	loc := normalize(location)
	if loc == "" {
		return Classification{Trip: true}
	}
	for _, m := range onlineMarkers {
		if strings.Contains(loc, m) {
			return Classification{Reason: ReasonOnline}
		}
	}
	if loc == "home" || loc == "thuis" || sameAddress(loc, normalize(home)) {
		return Classification{Reason: ReasonAtHome}
	}
	for _, p := range places {
		if loc == normalize(p.Name) || sameAddress(loc, normalize(p.Address)) {
			return Classification{Trip: true}
		}
	}
	return Classification{Trip: true, NeedsReview: !addressLike(loc)}
}

// sameAddress compares the first comma-separated segment (street and number),
// so "Damrak 1, Amsterdam, Netherlands" matches "Damrak 1, 1012 LG Amsterdam".
func sameAddress(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	first := func(s string) string { return strings.TrimSpace(strings.SplitN(s, ",", 2)[0]) }
	fa, fb := first(a), first(b)
	return fa == fb && strings.IndexFunc(fa, unicode.IsDigit) >= 0
}

func addressLike(s string) bool {
	return strings.Contains(s, ",") || strings.IndexFunc(s, unicode.IsDigit) >= 0
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

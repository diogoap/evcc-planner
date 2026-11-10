package forecast

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/localtime"
)

// ArticleSource fetches the outlook page.
type ArticleSource interface {
	Name() string // live or stub
	URL() string
	Fetch(ctx context.Context) ([]byte, error)
}

// HTTPArticle downloads the Tibber outlook page.
type HTTPArticle struct {
	PageURL string
	HTTP    *http.Client
}

// Name identifies the source.
func (HTTPArticle) Name() string { return "live" }

// URL is the page address.
func (a HTTPArticle) URL() string { return a.PageURL }

// Fetch downloads the page.
func (a HTTPArticle) Fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.PageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "evcc-planner (+https://github.com/diogoap/evcc-planner)")
	req.Header.Set("Accept", "text/html")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("outlook page unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("outlook page returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

var (
	dropBlocks = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template|head)\b.*?</(script|style|noscript|svg|template|head)>`)
	blockTags  = regexp.MustCompile(`(?i)<\s*/?\s*(p|div|h[1-6]|li|ul|ol|br|tr|td|th|table|section|article|header|footer|nav|main|blockquote)\b[^>]*>`)
	comments   = regexp.MustCompile(`(?s)<!--.*?-->`)
	anyTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	headingRe  = regexp.MustCompile(`(?i)^prognose voor\b`)
	stopRe     = regexp.MustCompile(`(?i)^(blijf op de hoogte|veelgestelde vragen|word een held|bereken je maandbedrag)`)
)

// ArticleText cuts the outlook article out of the page: from the "Prognose
// voor ..." heading to the first section after it that is not part of the
// article. Pages without that heading are rejected rather than sending
// unrelated text to the model.
func ArticleText(page []byte) (string, error) {
	s := dropBlocks.ReplaceAllString(string(page), " ")
	s = comments.ReplaceAllString(s, "")
	s = blockTags.ReplaceAllString(s, "\n")
	// Inline tags (and React's text separators) sit inside sentences.
	s = anyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	started := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(strings.ReplaceAll(line, " ", " ")), " ")
		if line == "" {
			continue
		}
		if !started {
			if !headingRe.MatchString(line) {
				continue
			}
			started = true
		} else if stopRe.MatchString(line) {
			break
		}
		lines = append(lines, line)
	}
	if !started {
		return "", errors.New(`outlook article not found (no "Prognose voor" heading); the page layout may have changed`)
	}
	text := strings.Join(lines, "\n")
	if len(text) > 12000 {
		text = text[:12000]
	}
	return text, nil
}

var (
	periodRe = regexp.MustCompile(`(?i)prognose voor\s+(\d{1,2})(?:\s+([a-z]+)\.?)?\s*(?:t/m|tot en met|-|–)\s*(\d{1,2})\s+([a-z]+)`)
	months   = map[string]time.Month{
		"jan": 1, "januari": 1, "feb": 2, "februari": 2, "mrt": 3, "maart": 3, "apr": 4, "april": 4, "mei": 5,
		"jun": 6, "juni": 6, "jul": 7, "juli": 7, "aug": 8, "augustus": 8, "sep": 9, "sept": 9, "september": 9,
		"okt": 10, "oktober": 10, "nov": 11, "november": 11, "dec": 12, "december": 12,
	}
)

// HeadingPeriod parses "Prognose voor 9 t/m 15 nov" (also across months,
// "28 okt t/m 3 nov"). The article has no year; the one closest to the fetch
// date is used.
func HeadingPeriod(text string, fetched time.Time, loc *time.Location) (string, string, bool) {
	m := periodRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	fromDay, _ := strconv.Atoi(m[1])
	toDay, _ := strconv.Atoi(m[3])
	toMonth, ok := months[strings.ToLower(m[4])]
	if !ok {
		return "", "", false
	}
	fromMonth := toMonth
	if m[2] != "" {
		if fm, ok := months[strings.ToLower(m[2])]; ok {
			fromMonth = fm
		}
	} else if fromDay > toDay {
		fromMonth = toMonth - 1
	}
	ref := localtime.StartOfDay(fetched, loc)
	var best time.Time
	for _, y := range []int{ref.Year() - 1, ref.Year(), ref.Year() + 1} {
		to := time.Date(y, toMonth, toDay, 0, 0, 0, 0, loc)
		if best.IsZero() || absDays(to.Sub(ref)) < absDays(best.Sub(ref)) {
			best = to
		}
	}
	from := time.Date(best.Year(), fromMonth, fromDay, 0, 0, 0, 0, loc)
	if from.After(best) {
		from = from.AddDate(-1, 0, 0)
	}
	if from.Day() != fromDay || best.Day() != toDay {
		return "", "", false // e.g. 31 nov
	}
	return from.Format(localtime.DateLayout), best.Format(localtime.DateLayout), true
}

func absDays(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// StubArticle generates a demo outlook page for the current week, in the
// same layout as the real page: a windy Tuesday and a calm Thursday, in line
// with the EVCC stub's prices.
type StubArticle struct {
	Clock    clock.Clock
	Location *time.Location
}

// Name identifies the source.
func (StubArticle) Name() string { return "stub" }

// URL is a placeholder address.
func (StubArticle) URL() string { return "stub://tibber-outlook" }

var dutchMonths = []string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"}

// Fetch returns the demo page.
func (a StubArticle) Fetch(ctx context.Context) ([]byte, error) {
	today := localtime.StartOfDay(a.Clock.Now(), a.Location)
	monday := localtime.AddDays(today, 1-localtime.ISOWeekday(today))
	sunday := localtime.AddDays(monday, 6)
	period := fmt.Sprintf("%d t/m %d %s", monday.Day(), sunday.Day(), dutchMonths[sunday.Month()-1])
	if monday.Month() != sunday.Month() {
		period = fmt.Sprintf("%d %s t/m %d %s", monday.Day(), dutchMonths[monday.Month()-1], sunday.Day(), dutchMonths[sunday.Month()-1])
	}
	page := `<!DOCTYPE html><html lang="nl"><head><title>Demo outlook</title><script>var x = "Prognose voor niets";</script></head>
<body><nav><a href="/">Energiecontract</a><a href="/">Producten</a></nav>
<h1>Wekelijkse stroomprijs prognose</h1><p>Demo-artikel met dezelfde opbouw als de echte pagina.</p>
<h2>Prognose voor ` + period + `</h2>
<p>Op basis van verschillende bronnen verwachten we een week met wisselende wind.</p>
<h3>📊 Prijzen: Lager dan vorige week</h3>
<p>Dinsdag waait het stevig en zakt de prijs naar circa €70/MWh. Donderdag valt de wind weg en lopen de prijzen op tot zo'n €140/MWh.</p>
<h3>💨 Wind: Wisselvallig</h3>
<p>Dinsdag rond 6,5 GW, donderdag slechts 1,2 GW. Met een weekgemiddelde van 3,8 GW zitten we iets onder normaal (4,2 GW).</p>
<h3>☀️ Zon: Najaarsdienst</h3>
<p>Met gemiddeld 1,0 GW doet de zonne-opwek wat we rond deze tijd mogen verwachten (de seizoensnorm ligt op 1,0 GW).</p>
<h3>Wat betekent dit voor jou?</h3>
<p>Weekgemiddelde: €0,100/kWh excl. (€0,250/kWh incl.)</p>
<p>Duurste dag: Donderdag (~€140/MWh excl.)</p>
<p>Goedkoopste dag: Dinsdag (~€70/MWh excl.)</p>
<h3>Tip van de week</h3><p>Laad je auto op dinsdag.</p>
<h2>Blijf op de hoogte met de Tibber-app</h2><p>Download onze app.</p>
<h2>Veelgestelde vragen over onze prijsprognose</h2><p>De prognose voor morgen verschijnt rond 13:00.</p>
</body></html>`
	return []byte(page), nil
}

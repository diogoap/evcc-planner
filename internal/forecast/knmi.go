package forecast

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/localtime"
)

// WeatherSchemaVersion identifies the stored weather payload contract.
const WeatherSchemaVersion = "knmi-outlook/1"

// Weather is KNMI's multi-day outlook for the Netherlands, normalized into
// dated records. Values KNMI does not give stay nil.
type Weather struct {
	SchemaVersion string       `json:"schemaVersion"`
	Source        string       `json:"source"`
	SourceFile    string       `json:"sourceFile"`
	IssuedAt      *time.Time   `json:"issuedAt"`
	FetchedAt     time.Time    `json:"fetchedAt"`
	Summary       string       `json:"summary"`
	LongRange     string       `json:"longRange"`
	Days          []WeatherDay `json:"days"`
}

// WeatherDay is one day of the outlook. Temperatures and precipitation are
// ranges (KNMI gives a low and high estimate).
type WeatherDay struct {
	Date            string   `json:"date"`
	SunshinePct     *int     `json:"sunshinePct"` // share of the possible sunshine
	PrecipChancePct *int     `json:"precipChancePct"`
	PrecipMinMm     *float64 `json:"precipMinMm"`
	PrecipMaxMm     *float64 `json:"precipMaxMm"`
	TempMinLowC     *float64 `json:"tempMinLowC"`
	TempMinHighC    *float64 `json:"tempMinHighC"`
	TempMaxLowC     *float64 `json:"tempMaxLowC"`
	TempMaxHighC    *float64 `json:"tempMaxHighC"`
	WindDir         string   `json:"windDir"`
	WindBft         *int     `json:"windBft"`
}

// Day returns the record for a date.
func (w *Weather) Day(date string) *WeatherDay {
	for i := range w.Days {
		if w.Days[i].Date == date {
			return &w.Days[i]
		}
	}
	return nil
}

// WeatherSource fetches the weather outlook.
type WeatherSource interface {
	Name() string // live or stub
	Fetch(ctx context.Context) (*Weather, error)
}

// KNMI reads the latest file of the outlook dataset ("middellange termijn
// verwachting", seven days from today) from the KNMI Open Data API.
type KNMI struct {
	BaseURL  string
	APIKey   string
	Dataset  string
	Version  string
	HTTP     *http.Client
	Location *time.Location
}

// Name identifies the source.
func (*KNMI) Name() string { return "live" }

// Fetch lists the newest files, downloads the newest XML, and parses it.
func (k *KNMI) Fetch(ctx context.Context) (*Weather, error) {
	base := fmt.Sprintf("%s/datasets/%s/versions/%s/files", strings.TrimRight(k.BaseURL, "/"),
		url.PathEscape(k.Dataset), url.PathEscape(k.Version))
	var list struct {
		Files []struct {
			Filename string    `json:"filename"`
			Created  time.Time `json:"created"`
		} `json:"files"`
	}
	if err := k.getJSON(ctx, base+"?maxKeys=10&sorting=desc&orderBy=created", &list); err != nil {
		return nil, err
	}
	var name string
	var created time.Time
	for _, f := range list.Files {
		if strings.HasSuffix(strings.ToLower(f.Filename), ".xml") {
			name, created = f.Filename, f.Created
			break
		}
	}
	if name == "" {
		return nil, errors.New("knmi: no XML file among the latest outlook files")
	}
	var link struct {
		URL string `json:"temporaryDownloadUrl"`
	}
	if err := k.getJSON(ctx, base+"/"+url.PathEscape(name)+"/url", &link); err != nil {
		return nil, err
	}
	// The download link is pre-signed; it takes no API key.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		return nil, errors.New("knmi: invalid download link")
	}
	resp, err := k.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("knmi: download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("knmi: download returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	w, err := ParseKNMIOutlook(data, k.Location)
	if err != nil {
		return nil, err
	}
	w.SourceFile = name
	if !created.IsZero() {
		w.IssuedAt = &created
	}
	return w, nil
}

func (k *KNMI) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", k.APIKey)
	resp, err := k.HTTP.Do(req)
	if err != nil {
		return errors.New("knmi open data api unreachable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		msg := resp.Status
		if json.Unmarshal(data, &e) == nil && (e.Error != "" || e.Message != "") {
			msg = e.Error + e.Message
		}
		return fmt.Errorf("knmi: %s", clip(msg, 200))
	}
	return json.Unmarshal(data, out)
}

type knmiDoc struct {
	Generated string `xml:"generated,attr"`
	Body      struct {
		Fields []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
		} `xml:",any"`
	} `xml:"Middellange_x0020_en_x0020_lange_x0020_Termijn"`
}

// ParseKNMIOutlook reads the outlook XML: day N has fields such as
// zonneschijnkans_dagN, windkracht_dagN, and its date in dagN_dd/mm/yy.
func ParseKNMIOutlook(data []byte, loc *time.Location) (*Weather, error) {
	var doc knmiDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("knmi: parse outlook: %w", err)
	}
	f := map[string]string{}
	for _, fld := range doc.Body.Fields {
		f[fld.XMLName.Local] = strings.TrimSpace(fld.Value)
	}
	w := &Weather{
		SchemaVersion: WeatherSchemaVersion, Source: "KNMI outlook",
		Summary: clip(f["verwachting_meerdaagse"], 400), LongRange: clip(f["verwachting_lange_termijn_tekst"], 400),
		Days: []WeatherDay{},
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", doc.Generated, loc); err == nil {
		w.IssuedAt = &t
	}
	for n := 1; n <= 10; n++ {
		s := strconv.Itoa(n)
		dd, err1 := strconv.Atoi(f["dag"+s+"_dd"])
		mm, err2 := strconv.Atoi(f["dag"+s+"_mm"])
		yy, err3 := strconv.Atoi(f["dag"+s+"_yy"])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		date := time.Date(2000+yy, time.Month(mm), dd, 0, 0, 0, 0, loc)
		if date.Day() != dd || int(date.Month()) != mm {
			continue
		}
		w.Days = append(w.Days, WeatherDay{
			Date:            date.Format(localtime.DateLayout),
			SunshinePct:     intField(f, "zonneschijnkans_dag"+s, 0, 100),
			PrecipChancePct: intField(f, "neerslagkans_dag"+s, 0, 100),
			PrecipMinMm:     floatField(f, "neerslaghoeveelheid_min_dag"+s, 0, 300),
			PrecipMaxMm:     floatField(f, "neerslaghoeveelheid_max_dag"+s, 0, 300),
			TempMinLowC:     floatField(f, "minimumtemperatuur_min_dag"+s, -40, 45),
			TempMinHighC:    floatField(f, "minimumtemperatuur_max_dag"+s, -40, 45),
			TempMaxLowC:     floatField(f, "maximumtemperatuur_min_dag"+s, -40, 45),
			TempMaxHighC:    floatField(f, "maximumtemperatuur_max_dag"+s, -40, 45),
			WindDir:         clip(f["windrichting_dag"+s], 8),
			WindBft:         intField(f, "windkracht_dag"+s, 0, 12),
		})
	}
	if len(w.Days) == 0 {
		return nil, errors.New("knmi: the outlook has no dated days")
	}
	return w, nil
}

func floatField(f map[string]string, key string, lo, hi float64) *float64 {
	v, err := strconv.ParseFloat(strings.ReplaceAll(f[key], ",", "."), 64)
	if err != nil || v < lo || v > hi {
		return nil
	}
	return &v
}

func intField(f map[string]string, key string, lo, hi int) *int {
	v, err := strconv.Atoi(f[key])
	if err != nil || v < lo || v > hi {
		return nil
	}
	return &v
}

// StubKNMI returns a demo outlook from today: a windy Tuesday, a calm and
// sunny Thursday, as in the demo price outlook.
type StubKNMI struct {
	Clock    clock.Clock
	Location *time.Location
}

// Name identifies the source.
func (StubKNMI) Name() string { return "stub" }

// Fetch returns the demo outlook.
func (s StubKNMI) Fetch(ctx context.Context) (*Weather, error) {
	now := s.Clock.Now()
	today := localtime.StartOfDay(now, s.Location)
	byWeekday := map[int]struct{ sun, rain, bft, tmin, tmax int }{
		1: {20, 30, 3, 4, 9}, 2: {30, 50, 5, 5, 10}, 3: {40, 20, 4, 3, 9}, 4: {70, 10, 2, 1, 8},
		5: {50, 20, 3, 2, 9}, 6: {30, 40, 4, 4, 10}, 7: {20, 60, 5, 6, 11},
	}
	issued := now.Add(-2 * time.Hour)
	w := &Weather{
		SchemaVersion: WeatherSchemaVersion, Source: "KNMI outlook (stub)", SourceFile: "stub.xml",
		IssuedAt: &issued, Summary: "Demo outlook: windy Tuesday, calm and sunny Thursday.", Days: []WeatherDay{},
	}
	dirs := []string{"ZW", "W", "NW", "N", "NO", "O", "Z"}
	for i := 0; i < 7; i++ {
		day := localtime.AddDays(today, i)
		v := byWeekday[localtime.ISOWeekday(day)]
		tmin, tmax := float64(v.tmin), float64(v.tmax)
		rain := 0.0
		w.Days = append(w.Days, WeatherDay{
			Date: day.Format(localtime.DateLayout), SunshinePct: &v.sun, PrecipChancePct: &v.rain,
			PrecipMinMm: &rain, PrecipMaxMm: &rain, TempMinLowC: &tmin, TempMinHighC: &tmin,
			TempMaxLowC: &tmax, TempMaxHighC: &tmax, WindDir: dirs[i], WindBft: &v.bft,
		})
	}
	return w, nil
}

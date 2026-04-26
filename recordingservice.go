package main

import (
	"errors"
	"os"

	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/store"
)

const SettingRecordSessions = "record_sessions"

// CastEvent is the over-the-wire shape of a single playback event. The cast
// internal type uses a custom MarshalJSON for asciinema compatibility, which
// the Wails binding generator can't introspect — this struct is the "wire"
// view used when handing events to the frontend.
type CastEvent struct {
	Offset float64 `json:"offset"`
	Kind   string  `json:"kind"`
	Data   string  `json:"data"`
}

type RecordingDetail struct {
	store.Recording
	Width  int         `json:"width"`
	Height int         `json:"height"`
	Events []CastEvent `json:"events"`
}

// SearchHit ties a recording to one or more matched lines for the search UI.
type SearchHit struct {
	Recording store.Recording   `json:"recording"`
	Matches   []recorder.Match  `json:"matches"`
}

type RecordingService struct {
	store    *store.Recordings
	settings *store.Settings
}

func NewRecordingService(s *store.Recordings, st *store.Settings) *RecordingService {
	return &RecordingService{store: s, settings: st}
}

func (s *RecordingService) IsEnabled() (bool, error) {
	v, err := s.settings.GetPlain(SettingRecordSessions)
	if err != nil {
		return false, err
	}
	return v == "1", nil
}

func (s *RecordingService) SetEnabled(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.settings.SetPlain(SettingRecordSessions, v)
}

func (s *RecordingService) List() ([]store.Recording, error) {
	return s.store.List(200)
}

func (s *RecordingService) Get(id string) (RecordingDetail, error) {
	rec, err := s.store.Get(id)
	if err != nil {
		return RecordingDetail{}, err
	}
	header, events, err := recorder.ParseFile(rec.Path)
	if err != nil {
		return RecordingDetail{}, err
	}
	wire := make([]CastEvent, 0, len(events))
	for _, e := range events {
		wire = append(wire, CastEvent{Offset: e.Offset, Kind: e.Kind, Data: e.Data})
	}
	return RecordingDetail{
		Recording: rec,
		Width:     header.Width,
		Height:    header.Height,
		Events:    wire,
	}, nil
}

func (s *RecordingService) Delete(id string) error {
	rec, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(id); err != nil {
		return err
	}
	_ = os.Remove(rec.Path)
	return nil
}

// Search greps every stored recording for the substring (case-insensitive)
// and returns hits grouped per recording. Bounded at maxHitsPerRecording per
// recording so a noisy match doesn't blow up the response.
func (s *RecordingService) Search(query string) ([]SearchHit, error) {
	if query == "" {
		return nil, errors.New("query required")
	}
	recs, err := s.store.List(500)
	if err != nil {
		return nil, err
	}
	const maxHitsPerRecording = 20
	out := []SearchHit{}
	for _, r := range recs {
		matches, err := recorder.SearchFile(r.Path, query)
		if err != nil || len(matches) == 0 {
			continue
		}
		if len(matches) > maxHitsPerRecording {
			matches = matches[:maxHitsPerRecording]
		}
		out = append(out, SearchHit{Recording: r, Matches: matches})
	}
	return out, nil
}

// ExportPath returns the on-disk path so the frontend can hand it to the OS
// "reveal in folder" / save-as flow. We expose the path rather than the
// bytes so a multi-MB export doesn't traverse the JSON bridge.
func (s *RecordingService) ExportPath(id string) (string, error) {
	rec, err := s.store.Get(id)
	if err != nil {
		return "", err
	}
	return rec.Path, nil
}

// ReadCastFile streams the raw cast bytes for a smaller-export case (e.g.
// SOC2 evidence bundles). 50MB cap to keep the bridge sane.
func (s *RecordingService) ReadCastFile(id string) (string, error) {
	rec, err := s.store.Get(id)
	if err != nil {
		return "", err
	}
	const maxBytes = 50 * 1024 * 1024
	info, err := os.Stat(rec.Path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxBytes {
		return "", errors.New("recording exceeds 50MB cap; use ExportPath to copy from disk")
	}
	b, err := os.ReadFile(rec.Path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

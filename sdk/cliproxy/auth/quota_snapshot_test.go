package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var snapshotNow = time.Date(2026, 10, 19, 17, 30, 0, 0, time.UTC)

func window(id WindowID, used float64, observedAt time.Time, section SectionID, source Source) QuotaWindowObs {
	return QuotaWindowObs{ID: id, Used: KnownPercent(used), Period: time.Hour, Section: section, Source: source, ObservedAt: observedAt}
}

func snapshotWindow(s QuotaSnapshot, id WindowID, scope string) (QuotaWindowObs, bool) {
	for _, w := range s.Windows {
		if w.ID == id && w.ModelScope == scope {
			return w, true
		}
	}
	return QuotaWindowObs{}, false
}

func TestQuotaSnapshotApplyStateModel(t *testing.T) {
	earlier := snapshotNow.Add(-time.Hour)
	later := snapshotNow.Add(time.Hour)
	tests := []struct {
		name   string
		before []QuotaObservation
		obs    QuotaObservation
		want   func(t *testing.T, s QuotaSnapshot, changed bool)
	}{
		{
			name: "joins on id and scope",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: earlier, Windows: []QuotaWindowObs{
				window("seven_day", 10, earlier, SectionHeaders, SourceHeaders),
			}}},
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				{ID: "seven_day", ModelScope: "fable", Used: KnownPercent(20), Section: SectionHeaders, Source: SourceHeaders, ObservedAt: snapshotNow},
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				if !changed || len(s.Windows) != 2 {
					t.Fatalf("scoped window must not replace the credential-wide one: %+v", s.Windows)
				}
			},
		},
		{
			name: "newer observed_at wins regardless of arrival order",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: later, Windows: []QuotaWindowObs{
				window("five_hour", 90, later, SectionHeaders, SourceHeaders),
			}}},
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: snapshotNow, Sections: map[SectionID]SectionStatus{SectionClaudeUsage: SectionOK}, Windows: []QuotaWindowObs{
				window("five_hour", 10, snapshotNow, SectionClaudeUsage, SourcePoll),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				w, _ := snapshotWindow(s, "five_hour", "")
				if used, _ := w.Used.Get(); used != 90 || changed {
					t.Fatalf("older poll must lose to the newer header window: %+v changed=%v", w, changed)
				}
			},
		},
		{
			name: "poll beats headers on a tie",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				window("five_hour", 50, snapshotNow, SectionClaudeUsage, SourcePoll),
			}}},
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				window("five_hour", 60, snapshotNow, SectionHeaders, SourceHeaders),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				w, _ := snapshotWindow(s, "five_hour", "")
				if used, _ := w.Used.Get(); used != 50 || w.Source != SourcePoll || changed {
					t.Fatalf("poll must win the tie: %+v", w)
				}
			},
		},
		{
			name: "headers keep stored windows they do not carry",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: earlier, Windows: []QuotaWindowObs{
				window("five_hour", 10, earlier, SectionClaudeUsage, SourcePoll),
				window("seven_day", 20, earlier, SectionClaudeUsage, SourcePoll),
			}}},
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				window("five_hour", 30, snapshotNow, SectionHeaders, SourceHeaders),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				if _, ok := snapshotWindow(s, "seven_day", ""); !ok || len(s.Windows) != 2 {
					t.Fatalf("partial headers dropped a window: %+v", s.Windows)
				}
			},
		},
		{
			name: "an ok section drops its windows the poll did not return",
			before: []QuotaObservation{{Provider: ProviderCodex, Source: SourcePoll, ObservedAt: earlier, Sections: map[SectionID]SectionStatus{SectionCodexUsage: SectionOK}, Windows: []QuotaWindowObs{
				window("primary", 10, earlier, SectionCodexUsage, SourcePoll),
				window("additional.GPT-5.3-Codex-Spark.primary", 20, earlier, SectionCodexUsage, SourcePoll),
				window("secondary", 5, earlier, SectionHeaders, SourceHeaders),
			}}},
			obs: QuotaObservation{Provider: ProviderCodex, Source: SourcePoll, ObservedAt: snapshotNow, Sections: map[SectionID]SectionStatus{SectionCodexUsage: SectionOK}, Windows: []QuotaWindowObs{
				window("primary", 15, snapshotNow, SectionCodexUsage, SourcePoll),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				if _, ok := snapshotWindow(s, "additional.GPT-5.3-Codex-Spark.primary", ""); ok {
					t.Fatalf("retired additional limit survived: %+v", s.Windows)
				}
				if _, ok := snapshotWindow(s, "secondary", ""); !ok {
					t.Fatalf("header-sourced window of another section was dropped: %+v", s.Windows)
				}
			},
		},
		{
			name: "a failed section keeps its windows",
			before: []QuotaObservation{{Provider: ProviderXAI, Source: SourcePoll, ObservedAt: earlier, Sections: map[SectionID]SectionStatus{SectionXAIWeekly: SectionOK}, Windows: []QuotaWindowObs{
				window("weekly", 42, earlier, SectionXAIWeekly, SourcePoll),
			}, Extras: XAIExtras{Access: XAIAccessGrokCLI, Weekly: OKSection(XAIWeekly{}, earlier)}}},
			obs: QuotaObservation{Provider: ProviderXAI, Source: SourcePoll, ObservedAt: snapshotNow, Sections: map[SectionID]SectionStatus{SectionXAIWeekly: SectionFailed}, Extras: XAIExtras{Weekly: FailedSection[XAIWeekly]("503", snapshotNow)}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				w, ok := snapshotWindow(s, "weekly", "")
				if used, _ := w.Used.Get(); !ok || used != 42 || !w.ObservedAt.Equal(earlier) {
					t.Fatalf("failed weekly section lost its last good window: %+v", s.Windows)
				}
				weekly := s.Extras.(XAIExtras).Weekly
				if weekly.Status != SectionFailed || weekly.Value == nil || !weekly.ValueAt.Equal(earlier) || weekly.Error != "503" || !weekly.CheckedAt.Equal(snapshotNow) {
					t.Fatalf("failed section must keep the last good value and mark stale: %+v", weekly)
				}
			},
		},
		{
			name:   "an absent section keeps the old field",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: earlier, Extras: ClaudeExtras{Plan: OKSection("plan_max", earlier)}}},
			obs:    QuotaObservation{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: snapshotNow, Extras: ClaudeExtras{ExtraUsage: OKSection(ClaudeExtraUsage{Enabled: true}, snapshotNow)}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				extras := s.Extras.(ClaudeExtras)
				if extras.Plan.Value == nil || *extras.Plan.Value != "plan_max" || extras.ExtraUsage.Value == nil {
					t.Fatalf("absent plan erased or extra usage missing: %+v", extras)
				}
			},
		},
		{
			name: "a window not stored is added",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: earlier, Windows: []QuotaWindowObs{
				window("five_hour", 10, earlier, SectionHeaders, SourceHeaders),
			}}},
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				{ID: "seven_day_fable", ModelScope: "fable", Section: SectionHeaders, Source: SourceHeaders, ObservedAt: snapshotNow},
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				w, ok := snapshotWindow(s, "seven_day_fable", "fable")
				if !ok || !changed {
					t.Fatalf("new scoped window missing: %+v", s.Windows)
				}
				if _, known := w.Used.Get(); known {
					t.Fatalf("unknown percent must stay unknown: %+v", w)
				}
			},
		},
		{
			name: "a provider change resets the snapshot",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: earlier, Windows: []QuotaWindowObs{
				window("five_hour", 10, earlier, SectionHeaders, SourceHeaders),
			}, Extras: ClaudeExtras{Plan: OKSection("plan_max", earlier)}}},
			obs: QuotaObservation{Provider: ProviderCodex, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				window("primary", 1, snapshotNow, SectionHeaders, SourceHeaders),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				if s.Provider != ProviderCodex || len(s.Windows) != 1 || s.Windows[0].ID != "primary" || s.Extras != nil {
					t.Fatalf("snapshot not reset on provider change: %+v", s)
				}
			},
		},
		{
			name:   "extras of another kind are ignored",
			before: []QuotaObservation{{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: earlier, Extras: ClaudeExtras{Plan: OKSection("plan_max", earlier)}}},
			obs:    QuotaObservation{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: snapshotNow, Extras: CodexExtras{Plan: OKSection("pro", snapshotNow)}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				if _, ok := s.Extras.(ClaudeExtras); !ok || changed {
					t.Fatalf("foreign extras applied: %+v", s.Extras)
				}
			},
		},
		{
			name: "windows sort by class order then id",
			obs: QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
				window("seven_day_opus", 1, snapshotNow, SectionClaudeUsage, SourcePoll),
				{ID: "seven_day_fable", ModelScope: "fable", Used: KnownPercent(1), Section: SectionHeaders, Source: SourceHeaders, ObservedAt: snapshotNow},
				window("seven_day", 1, snapshotNow, SectionHeaders, SourceHeaders),
				window("five_hour", 1, snapshotNow, SectionHeaders, SourceHeaders),
			}},
			want: func(t *testing.T, s QuotaSnapshot, changed bool) {
				var ids []WindowID
				for _, w := range s.Windows {
					ids = append(ids, w.ID)
				}
				want := []WindowID{"five_hour", "seven_day", "seven_day_fable", "seven_day_opus"}
				for i := range want {
					if ids[i] != want[i] {
						t.Fatalf("order = %v, want %v", ids, want)
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s QuotaSnapshot
			for _, obs := range tc.before {
				s.apply(obs)
			}
			changed := s.apply(tc.obs)
			tc.want(t, s, changed)
		})
	}
}

func TestQuotaSnapshotUpdatedAtFollowsObservedAt(t *testing.T) {
	var s QuotaSnapshot
	s.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
		window("five_hour", 1, snapshotNow, SectionHeaders, SourceHeaders),
	}})
	s.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow.Add(-time.Hour), Windows: []QuotaWindowObs{
		window("seven_day", 1, snapshotNow.Add(-time.Hour), SectionHeaders, SourceHeaders),
	}})
	if !s.UpdatedAt.Equal(snapshotNow) {
		t.Fatalf("UpdatedAt = %v, want %v", s.UpdatedAt, snapshotNow)
	}
}

func TestQuotaSnapshotRoutingWindows(t *testing.T) {
	var s QuotaSnapshot
	s.apply(QuotaObservation{Provider: ProviderCodex, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
		{ID: "primary", Period: 5 * time.Hour, LimitReached: true, ResetAt: snapshotNow.Add(time.Hour), Section: SectionHeaders, ObservedAt: snapshotNow},
		{ID: "secondary", Period: 7 * 24 * time.Hour, Section: SectionHeaders, ObservedAt: snapshotNow},
		{ID: "code_review.primary", Used: KnownPercent(5), Period: 5 * time.Hour, Section: SectionHeaders, ObservedAt: snapshotNow},
		{ID: "additional.x.primary", Used: KnownPercent(5), Period: 5 * time.Hour, ResetAt: snapshotNow.Add(-time.Minute), Section: SectionHeaders, ObservedAt: snapshotNow},
	}})
	got := s.RoutingWindows(snapshotNow)
	if len(got) != 1 || got[0].Class != "codex:5h" || got[0].UsedPercent != 100 {
		t.Fatalf("routing windows = %+v, want one codex:5h at 100", got)
	}
}

func TestQuotaSnapshotGolden(t *testing.T) {
	var s QuotaSnapshot
	fiveHourAt := time.Date(2026, 10, 19, 17, 30, 0, 0, time.UTC)
	pollAt := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	fableAt := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	s.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: fableAt, Windows: []QuotaWindowObs{
		{ID: "seven_day_fable", ModelScope: "fable", Period: 7 * 24 * time.Hour, ResetAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), Section: SectionHeaders, Source: SourceHeaders, ObservedAt: fableAt},
	}})
	s.apply(QuotaObservation{
		Provider: ProviderClaude, Source: SourcePoll, ObservedAt: pollAt,
		Sections: map[SectionID]SectionStatus{SectionClaudeUsage: SectionOK},
		Windows: []QuotaWindowObs{
			{ID: "five_hour", Used: KnownPercent(10), Period: 5 * time.Hour, Section: SectionClaudeUsage, Source: SourcePoll, ObservedAt: pollAt},
			{ID: "seven_day", Used: KnownPercent(60.5), Period: 7 * 24 * time.Hour, ResetAt: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC), Section: SectionClaudeUsage, Source: SourcePoll, ObservedAt: pollAt},
		},
		Extras: ClaudeExtras{Plan: OKSection("plan_max", pollAt)},
		Poll:   &PollStatus{At: pollAt, Trigger: PollBackground},
	})
	s.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: fiveHourAt, Windows: []QuotaWindowObs{
		{ID: "five_hour", Used: KnownPercent(25), Period: 5 * time.Hour, ResetAt: time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC), Section: SectionHeaders, Source: SourceHeaders, ObservedAt: fiveHourAt},
	}})

	got, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "quota_snapshot_claude.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("snapshot JSON differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}

	var decoded QuotaSnapshot
	if err := json.Unmarshal(want, &decoded); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	again, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatalf("marshal decoded: %v", err)
	}
	if !bytes.Equal(append(again, '\n'), want) {
		t.Fatalf("golden does not round-trip\n--- got ---\n%s", again)
	}
	if _, known := decoded.Windows[2].Used.Get(); known || decoded.Windows[2].ModelScope != "fable" {
		t.Fatalf("decoded fable window = %+v", decoded.Windows[2])
	}
}

func TestQuotaSnapshotLegacyRestore(t *testing.T) {
	records, err := readCooldownStateFile(context.Background(), filepath.Join("testdata", "legacy_claude.cds"))
	if err != nil {
		t.Fatalf("read legacy fixture: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("fixture has no records")
	}
	now := time.Date(2026, 10, 19, 18, 16, 29, 0, time.UTC)
	m := NewManager(nil, nil, nil)
	const authID = "claude-legacy@example.com.json"
	if _, err := m.Register(context.Background(), &Auth{ID: authID, Provider: "claude", Status: StatusActive}); err != nil {
		t.Fatalf("register: %v", err)
	}
	m.mu.Lock()
	for _, record := range records {
		m.restoreQuotaObservationLocked(record, now)
	}
	snapshot := m.auths[authID].QuotaSnapshot.Clone()
	m.mu.Unlock()

	legacyPercent := func(model, header string) float64 {
		for _, record := range records {
			if record.Model != model {
				continue
			}
			value, err := strconv.ParseFloat(record.legacySignals[http.CanonicalHeaderKey("Anthropic-Ratelimit-Unified-"+header+"-Utilization")], 64)
			if err != nil {
				t.Fatalf("legacy %s %s utilization: %v", model, header, err)
			}
			return value * 100
		}
		t.Fatalf("no legacy record for model %q", model)
		return 0
	}
	cases := []struct {
		id     WindowID
		scope  string
		model  string
		header string
	}{
		{id: "five_hour", header: "5h"},
		{id: "seven_day", header: "7d"},
		{id: "seven_day_fable", scope: "fable", model: "claude-fable-5-1", header: "7d_oi"},
	}
	for _, tc := range cases {
		w, ok := snapshotWindow(snapshot, tc.id, tc.scope)
		if !ok {
			t.Fatalf("window %s/%q missing after restore: %+v", tc.id, tc.scope, snapshot.Windows)
		}
		used, known := w.Used.Get()
		if want := legacyPercent(tc.model, tc.header); !known || used != clampPercent(want) {
			t.Fatalf("%s used = %v (known %v), want %v", tc.id, used, known, want)
		}
		if w.Source != SourceHeaders {
			t.Fatalf("%s source = %q, want headers so the first poll overrides it", tc.id, w.Source)
		}
	}
	if snapshot.Provider != ProviderClaude {
		t.Fatalf("provider = %q", snapshot.Provider)
	}
}

func TestQuotaSnapshotMergeKeepsNewestOfBoth(t *testing.T) {
	var stored, incoming QuotaSnapshot
	earlier := snapshotNow.Add(-time.Hour)
	stored.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
		window("five_hour", 70, snapshotNow, SectionHeaders, SourceHeaders),
	}})
	incoming.apply(QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: earlier, Windows: []QuotaWindowObs{
		window("five_hour", 20, earlier, SectionHeaders, SourceHeaders),
		window("seven_day", 30, earlier, SectionHeaders, SourceHeaders),
	}})
	stored.merge(incoming)
	fiveHour, _ := snapshotWindow(stored, "five_hour", "")
	if used, _ := fiveHour.Used.Get(); used != 70 {
		t.Fatalf("older incoming window replaced the stored one: %+v", fiveHour)
	}
	if _, ok := snapshotWindow(stored, "seven_day", ""); !ok {
		t.Fatalf("incoming window missing after merge: %+v", stored.Windows)
	}
}

func TestQuotaSnapshotCloneIsIndependent(t *testing.T) {
	var s QuotaSnapshot
	s.apply(QuotaObservation{Provider: ProviderClaude, Source: SourcePoll, ObservedAt: snapshotNow, Windows: []QuotaWindowObs{
		window("five_hour", 1, snapshotNow, SectionClaudeUsage, SourcePoll),
	}, Extras: ClaudeExtras{Plan: OKSection("plan_max", snapshotNow)}, Poll: &PollStatus{At: snapshotNow}})
	c := s.Clone()
	c.Windows[0].Used = KnownPercent(99)
	*c.Extras.(ClaudeExtras).Plan.Value = "plan_free"
	c.LastPoll.Error = "x"
	if used, _ := s.Windows[0].Used.Get(); used != 1 || *s.Extras.(ClaudeExtras).Plan.Value != "plan_max" || s.LastPoll.Error != "" {
		t.Fatalf("clone shares state with the original: %+v", s)
	}
}

func BenchmarkObserveClaudeHeaders(b *testing.B) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.25")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1792441200")
	h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.6")
	h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "1792700000")
	h.Set("Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.12")
	h.Set("Anthropic-Ratelimit-Unified-7d_oi-Reset", "1792600000")
	h.Set("Anthropic-Ratelimit-Unified-Status", "allowed")
	var s QuotaSnapshot
	now := snapshotNow
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Second)
		s.ObserveResponseHeaders("claude", "claude-fable-5-1", h, now)
	}
}

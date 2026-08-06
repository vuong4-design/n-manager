package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestFetchTrialTypeClassification(t *testing.T) {
	previousConfig := AppConfig
	previousBase := NotionAPIBase
	previousClientOverride := chromeHTTPClientForTest
	AppConfig = DefaultConfig()
	t.Cleanup(func() {
		AppConfig = previousConfig
		NotionAPIBase = previousBase
		chromeHTTPClientForTest = previousClientOverride
	})

	now := time.Now()
	activeStart := now.Add(-time.Hour).UnixMilli()
	activeEnd := now.Add(24 * time.Hour).UnixMilli()
	expiredStart := now.Add(-48 * time.Hour).UnixMilli()
	expiredEnd := now.Add(-24 * time.Hour).UnixMilli()

	tests := []struct {
		name       string
		offerType  string
		days       int
		startMs    int64
		endMs      int64
		statusCode int
		malformed  bool
		want       string
		wantErr    bool
	}{
		{name: "active 14 day trial", offerType: "trial", days: 14, startMs: activeStart, endMs: activeEnd, want: TrialType14},
		{name: "active 30 day trial", offerType: "trial", days: 30, startMs: activeStart, endMs: activeEnd, want: TrialType30},
		{name: "unsupported 180 day trial", offerType: "trial", days: 180, startMs: activeStart, endMs: activeEnd, want: TrialTypeUnknown},
		{name: "paid offer", offerType: "subscription", days: 30, startMs: activeStart, endMs: activeEnd, want: TrialTypeUnknown},
		{name: "expired 14 day trial", offerType: "trial", days: 14, startMs: expiredStart, endMs: expiredEnd, want: TrialTypeUnknown},
		{name: "malformed response", malformed: true, want: TrialTypeUnknown, wantErr: true},
		{name: "upstream error", statusCode: http.StatusBadGateway, want: TrialTypeUnknown, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/getCustomerOffersReceived" {
					http.NotFound(w, r)
					return
				}
				var request struct {
					SpaceID string `json:"spaceId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if request.SpaceID != "space-test" {
					t.Fatalf("spaceId=%q want space-test", request.SpaceID)
				}
				if tt.statusCode != 0 {
					w.WriteHeader(tt.statusCode)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if tt.malformed {
					_, _ = w.Write([]byte(`{"broken":`))
					return
				}
				_, _ = fmt.Fprintf(w, `[{"startDateMs":%d,"endDateMs":%d,"offer":{"type":%q,"duration":{"days":%d}}}]`, tt.startMs, tt.endMs, tt.offerType, tt.days)
			}))
			defer server.Close()

			NotionAPIBase = server.URL
			chromeHTTPClientForTest = func(time.Duration) *http.Client { return server.Client() }
			got, err := FetchTrialType(&Account{SpaceID: "space-test", UserID: "user-test", TokenV2: "token-test"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchTrialType error=%v wantErr=%v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("FetchTrialType=%q want %q", got, tt.want)
			}
		})
	}
}

func TestCheckUserWorkspaceProfileIncludesTrialType(t *testing.T) {
	previousConfig := AppConfig
	previousBase := NotionAPIBase
	previousClientOverride := chromeHTTPClientForTest
	AppConfig = DefaultConfig()
	t.Cleanup(func() {
		AppConfig = previousConfig
		NotionAPIBase = previousBase
		chromeHTTPClientForTest = previousClientOverride
	})

	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/loadUserContent":
			_, _ = w.Write([]byte(`{"recordMap":{"user_root":{"user-test":{"value":{"value":{"space_views":["view-test"],"space_view_pointers":[{"spaceId":"space-test","id":"view-test"}]}}}},"space":{"space-test":{"value":{"value":{"id":"space-test","plan_type":"plus","settings":{}}}}}}}`))
		case "/getCustomerOffersReceived":
			_, _ = fmt.Fprintf(w, `[{"startDateMs":%d,"endDateMs":%d,"offer":{"type":"trial","duration":{"days":30}}}]`, now.Add(-time.Hour).UnixMilli(), now.Add(24*time.Hour).UnixMilli())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	NotionAPIBase = server.URL
	chromeHTTPClientForTest = func(time.Duration) *http.Client { return server.Client() }

	result, err := CheckUserWorkspaceProfile(&Account{SpaceID: "space-test", UserID: "user-test", TokenV2: "token-test"})
	if err != nil {
		t.Fatalf("CheckUserWorkspaceProfile: %v", err)
	}
	if result.TrialType != TrialType30 {
		t.Fatalf("trial type=%q want %q", result.TrialType, TrialType30)
	}
}

func TestTrialTypePersistsAndIsExposed(t *testing.T) {
	dir := t.TempDir()
	email := "trial@example.com"
	path := seedAccountFile(t, dir, email)
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{
		UserEmail:      email,
		UserID:         "u-" + email,
		SpaceID:        "s-" + email,
		TrialType:      TrialType14,
		TrialCheckedAt: &now,
	}
	if err := saveAccountFile(dir, account); err != nil {
		t.Fatalf("saveAccountFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read account file: %v", err)
	}
	var persisted map[string]interface{}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("parse account file: %v", err)
	}
	if persisted["trial_type"] != TrialType14 {
		t.Fatalf("persisted trial_type=%v want %s", persisted["trial_type"], TrialType14)
	}
	if persisted["trial_checked_at"] != now.Format(time.RFC3339) {
		t.Fatalf("persisted trial_checked_at=%v want %s", persisted["trial_checked_at"], now.Format(time.RFC3339))
	}

	pool := NewAccountPool()
	if err := pool.LoadFromDir(dir); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	details := pool.GetAccountDetails()
	if len(details) != 1 {
		t.Fatalf("details len=%d want 1", len(details))
	}
	if details[0]["trial_type"] != TrialType14 {
		t.Fatalf("API trial_type=%v want %s", details[0]["trial_type"], TrialType14)
	}
	if details[0]["trial_checked_at"] != now.Format(time.RFC3339) {
		t.Fatalf("API trial_checked_at=%v want %s", details[0]["trial_checked_at"], now.Format(time.RFC3339))
	}
}

func TestApplyWorkspaceProfileNormalizesUnsupportedTrial(t *testing.T) {
	account := &Account{UserEmail: "unknown@example.com"}
	pool := NewAccountPool()
	pool.applyWorkspaceProfile(account, WorkspaceProbeResult{Count: 1, TrialType: "trial_180"})
	profile := account.profileSnapshot()
	if profile.TrialType != TrialTypeUnknown {
		t.Fatalf("trial type=%q want %q", profile.TrialType, TrialTypeUnknown)
	}
	if profile.TrialCheckedAt == nil {
		t.Fatal("trial checked timestamp was not recorded")
	}
}

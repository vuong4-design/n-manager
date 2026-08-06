package proxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPStoreKeepsSecretsPrivateAndPreservesBlankUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp_servers.json")
	store, err := NewMCPStore(path)
	if err != nil {
		t.Fatalf("NewMCPStore: %v", err)
	}
	created, err := store.Upsert(MCPServerInput{
		Name:                       "Workspace MCP",
		URL:                        "https://mcp.example.test/server/",
		AuthType:                   MCPAuthBasic,
		Username:                   "user",
		Password:                   "private-password",
		RunWriteToolsAutomatically: true,
	})
	if err != nil {
		t.Fatalf("Upsert create: %v", err)
	}
	if !created.HasPassword || created.HasBearerToken {
		t.Fatalf("unexpected public secret flags: %#v", created)
	}
	publicJSON, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "private-password") {
		t.Fatalf("public response leaked password: %s", publicJSON)
	}

	updated, err := store.Upsert(MCPServerInput{
		ID:                         created.ID,
		Name:                       "Workspace MCP renamed",
		URL:                        "https://mcp.example.test/server",
		AuthType:                   MCPAuthBasic,
		Username:                   "user",
		Password:                   "",
		RunWriteToolsAutomatically: false,
	})
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if !updated.HasPassword {
		t.Fatal("blank update cleared saved password")
	}
	private, ok := store.Get(created.ID)
	if !ok || private.Password != "private-password" {
		t.Fatalf("private password was not preserved: %#v", private)
	}

	reloaded, err := NewMCPStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	private, ok = reloaded.Get(created.ID)
	if !ok || private.Password != "private-password" {
		t.Fatalf("reloaded secret mismatch: %#v", private)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil {
			t.Fatalf("stat: %v", err)
		} else if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("MCP store permissions are too broad: %o", info.Mode().Perm())
		}
	}
}

type capturedMCPRequest struct {
	Path string
	Body map[string]interface{}
}

func withMCPNotionServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	previousConfig := AppConfig
	previousBase := NotionAPIBase
	previousClientOverride := chromeHTTPClientForTest
	AppConfig = DefaultConfig()
	server := httptest.NewServer(handler)
	NotionAPIBase = server.URL
	chromeHTTPClientForTest = func(time.Duration) *http.Client { return server.Client() }
	t.Cleanup(func() {
		server.Close()
		AppConfig = previousConfig
		NotionAPIBase = previousBase
		chromeHTTPClientForTest = previousClientOverride
	})
	return server
}

func decodeMCPRequest(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", r.URL.Path, err)
	}
	return body
}

func TestInstallMCPServerCreatesAssignsConnectsAndEnablesWriteTools(t *testing.T) {
	var mu sync.Mutex
	requests := make([]capturedMCPRequest, 0, 6)
	withMCPNotionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeMCPRequest(t, r)
		mu.Lock()
		requests = append(requests, capturedMCPRequest{Path: r.URL.Path, Body: body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/validateMcpConnection":
			if body["serverUrl"] != "https://mcp.example.test/server" || body["spaceId"] != "space-1" {
				t.Fatalf("unexpected validation payload: %#v", body)
			}
			if _, exists := body["url"]; exists {
				t.Fatalf("legacy validation payload field remained: %#v", body)
			}
			headers, _ := body["authHeaders"].([]interface{})
			if len(headers) != 1 {
				t.Fatalf("auth headers=%#v", body["authHeaders"])
			}
			header, _ := headers[0].(map[string]interface{})
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
			if header["value"] != want {
				t.Fatalf("authorization=%v want %s", header["value"], want)
			}
			_, _ = w.Write([]byte(`{"success":true,"officialName":"Workspace MCP","tools":[{"name":"write_item","title":"Write item","description":"Writes an item","annotations":{"readOnlyHint":false,"openWorldHint":true,"destructiveHint":true},"inputSchema":{"type":"object"}}]}`))
		case "/loadUserContent":
			_, _ = w.Write([]byte(`{"recordMap":{"space_view":{"view-1":{"value":{"value":{"id":"view-1","space_id":"space-1","settings":{"agent_chat_modules":[]}}}}}}}`))
		case "/saveTransactions", "/saveTransactionsFanout", "/postWorkflowsMcpServerConnect":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))

	result, err := InstallMCPServer(&Account{
		UserID:        "user-1",
		SpaceID:       "space-1",
		SpaceViewID:   "view-1",
		TokenV2:       "token",
		ClientVersion: DefaultClientVersion,
	}, &MCPServerConfig{
		Name:                       "Workspace MCP",
		URL:                        "https://mcp.example.test/server",
		AuthType:                   MCPAuthBasic,
		Username:                   "user",
		Password:                   "pass",
		RunWriteToolsAutomatically: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPServer: %v", err)
	}
	if result.ModuleID == "" || !result.Created || !result.AssignedToNotionAgent || !result.Connected || !result.WriteToolsRunAutomatically {
		t.Fatalf("unexpected install result: %#v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.Path)
	}
	wantPaths := []string{
		"/validateMcpConnection",
		"/loadUserContent",
		"/saveTransactionsFanout",
		"/postWorkflowsMcpServerConnect",
		"/saveTransactionsFanout",
		"/saveTransactionsFanout",
	}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("request order=%v want %v", paths, wantPaths)
	}
	createJSON, _ := json.Marshal(requests[2].Body)
	if strings.Contains(string(createJSON), "inputSchema") {
		t.Fatalf("workflow module persisted validation input schema: %s", createJSON)
	}
	if !strings.Contains(string(createJSON), `"module_type":"mcpServer"`) || !strings.Contains(string(createJSON), "agentPersistenceHelpers.createAgentChatModule") {
		t.Fatalf("workflow module creation contract mismatch: %s", createJSON)
	}
	connectJSON, _ := json.Marshal(requests[3].Body)
	if !strings.Contains(string(connectJSON), `"integrationId"`) || !strings.Contains(string(connectJSON), `"initiationContext":"connect"`) {
		t.Fatalf("MCP connect contract mismatch: %s", connectJSON)
	}
	assignmentJSON, _ := json.Marshal(requests[4].Body)
	if !strings.Contains(string(assignmentJSON), `"path":["settings"]`) || !strings.Contains(string(assignmentJSON), `"agent_chat_modules"`) {
		t.Fatalf("Notion Agent assignment contract mismatch: %s", assignmentJSON)
	}
	updateJSON, _ := json.Marshal(requests[len(requests)-1].Body)
	if !strings.Contains(string(updateJSON), `"runWriteToolsAutomatically":true`) {
		t.Fatalf("write auto flag missing: %s", updateJSON)
	}
}

func TestInstallMCPServerReusesExistingModuleByURL(t *testing.T) {
	const moduleID = "module-existing"
	var paths []string
	withMCPNotionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeMCPRequest(t, r)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/validateMcpConnection":
			_, _ = w.Write([]byte(`{"tools":[{"name":"read_item","annotations":{"readOnlyHint":true}}],"icon":"🤖"}`))
		case "/loadUserContent":
			_, _ = w.Write([]byte(`{"recordMap":{"space_view":{"view-1":{"value":{"value":{"id":"view-1","space_id":"space-1","settings":{"agent_chat_modules":[{"pointer":{"table":"workflow_module","id":"module-existing","spaceId":"space-1"},"defaultEnabled":false}]}}}}}}}`))
		case "/syncRecordValuesSpaceInitial":
			requests, _ := body["requests"].([]interface{})
			if len(requests) != 1 {
				t.Fatalf("module fetch requests=%#v", body["requests"])
			}
			_, _ = w.Write([]byte(`{"__version__":3,"workflow_module":{"module-existing":{"value":{"value":{"id":"module-existing","space_id":"space-1","module_type":"mcpServer","version":2,"data":{"id":"module-existing","name":"Old name","serverUrl":"https://mcp.example.test/server/","tools":[]}}}}}}`))
		case "/postWorkflowsMcpServerConnect", "/saveTransactionsFanout":
			_, _ = w.Write([]byte(`{}`))
		case "/saveTransactions":
			t.Fatal("existing MCP unexpectedly created a duplicate workflow module")
		default:
			http.NotFound(w, r)
		}
	}))

	result, err := InstallMCPServer(&Account{
		UserID:        "user-1",
		SpaceID:       "space-1",
		SpaceViewID:   "view-1",
		TokenV2:       "token",
		ClientVersion: DefaultClientVersion,
	}, &MCPServerConfig{
		Name:                       "Renamed MCP",
		URL:                        "https://mcp.example.test/server",
		AuthType:                   MCPAuthNone,
		RunWriteToolsAutomatically: true,
	})
	if err != nil {
		t.Fatalf("InstallMCPServer: %v", err)
	}
	if result.ModuleID != moduleID || result.Created || !result.Connected {
		t.Fatalf("unexpected reused result: %#v", result)
	}
	want := []string{
		"/validateMcpConnection",
		"/loadUserContent",
		"/syncRecordValuesSpaceInitial",
		"/postWorkflowsMcpServerConnect",
		"/saveTransactionsFanout",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths=%v want %v", paths, want)
	}
}

func TestInstallMCPServerReturnsPartialStateWhenConnectFails(t *testing.T) {
	withMCPNotionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/validateMcpConnection":
			_, _ = w.Write([]byte(`{"tools":[{"name":"tool"}],"icon":"🤖"}`))
		case "/loadUserContent":
			_, _ = w.Write([]byte(`{"recordMap":{"space_view":{"view-1":{"value":{"value":{"id":"view-1","space_id":"space-1","settings":{"agent_chat_modules":[]}}}}}}}`))
		case "/postWorkflowsMcpServerConnect":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"name":"UnknownMcpError"}`))
		case "/saveTransactions", "/saveTransactionsFanout":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))

	result, err := InstallMCPServer(&Account{
		UserID:        "user-1",
		SpaceID:       "space-1",
		SpaceViewID:   "view-1",
		TokenV2:       "token",
		ClientVersion: DefaultClientVersion,
	}, &MCPServerConfig{
		Name:                       "Workspace MCP",
		URL:                        "https://mcp.example.test/server",
		AuthType:                   MCPAuthNone,
		RunWriteToolsAutomatically: true,
	})
	if err == nil {
		t.Fatal("connect failure was not surfaced")
	}
	var partial *MCPPartialInstallError
	if !strings.Contains(err.Error(), "UnknownMcpError") || !errors.As(err, &partial) {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
	if result.ModuleID == "" || !result.Created || !result.AssignedToNotionAgent || result.Connected {
		t.Fatalf("partial result lost completed state: %#v", result)
	}
}

func TestAccountBatchMCPInstallPreservesServerAcrossRetry(t *testing.T) {
	account := &Account{UserID: "user-1", UserEmail: "mcp@example.test", SpaceID: "space-1"}
	account.EnsureAccountID()
	pool := NewAccountPool()
	pool.accounts = []*Account{account}
	store, err := NewMCPStore("")
	if err != nil {
		t.Fatal(err)
	}
	server, err := store.Upsert(MCPServerInput{
		Name:                       "Workspace MCP",
		URL:                        "https://mcp.example.test/server",
		AuthType:                   MCPAuthBasic,
		Username:                   "user",
		Password:                   "pass",
		RunWriteToolsAutomatically: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewAccountBatchManager(pool, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	manager.SetMCPStore(store)

	originalInstaller := mcpInstaller
	calls := 0
	mcpInstaller = func(gotAccount *Account, gotServer *MCPServerConfig) (MCPInstallResult, error) {
		calls++
		if gotAccount != account || gotServer.ID != server.ID || gotServer.Password != "pass" {
			t.Fatalf("unexpected installer input: account=%#v server=%#v", gotAccount, gotServer)
		}
		if calls == 1 {
			result := MCPInstallResult{ModuleID: "module-1", Created: true, AssignedToNotionAgent: true, Connected: false, WriteToolsRunAutomatically: true}
			return result, &MCPPartialInstallError{Result: result, Err: errors.New("connect failed")}
		}
		return MCPInstallResult{ModuleID: "module-1", AssignedToNotionAgent: true, Connected: true, WriteToolsRunAutomatically: true}, nil
	}
	t.Cleanup(func() { mcpInstaller = originalInstaller })

	job, err := manager.StartMCPInstall(server.ID, []string{account.AccountID}, 1)
	if err != nil {
		t.Fatalf("StartMCPInstall: %v", err)
	}
	failed := waitForAccountBatchJob(t, manager, job.ID)
	if failed.MCPServerID != server.ID || failed.Failed != 1 || failed.Steps[0].ModuleID != "module-1" || failed.Steps[0].Connected == nil || *failed.Steps[0].Connected {
		t.Fatalf("unexpected failed MCP job: %#v", failed)
	}

	retry, err := manager.Retry(failed.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	finished := waitForAccountBatchJob(t, manager, retry.ID)
	if finished.MCPServerID != server.ID || finished.Succeeded != 1 || finished.Steps[0].ModuleID != "module-1" || finished.Steps[0].Connected == nil || !*finished.Steps[0].Connected {
		t.Fatalf("unexpected retried MCP job: %#v", finished)
	}
	if calls != 2 {
		t.Fatalf("installer calls=%d want 2", calls)
	}
}

func TestRemoveMCPServerUpdatesSettingsAndArchivesMatchingModule(t *testing.T) {
	var paths []string
	var removalBody map[string]interface{}
	withMCPNotionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeMCPRequest(t, r)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/loadUserContent":
			_, _ = w.Write([]byte(`{"recordMap":{"space_view":{"view-1":{"value":{"value":{"id":"view-1","space_id":"space-1","settings":{"notify_email_digest":true,"agent_chat_modules":[{"pointer":{"table":"workflow_module","id":"module-target","spaceId":"space-1"},"defaultEnabled":false},{"pointer":{"table":"workflow_module","id":"module-other","spaceId":"space-1"},"defaultEnabled":true}]}}}}}}}`))
		case "/syncRecordValuesSpaceInitial":
			_, _ = w.Write([]byte(`{"__version__":3,"workflow_module":{"module-target":{"value":{"value":{"id":"module-target","space_id":"space-1","module_type":"mcpServer","alive":true,"data":{"id":"module-target","serverUrl":"https://mcp.example.test/server/"}}}},"module-other":{"value":{"value":{"id":"module-other","space_id":"space-1","module_type":"mcpServer","alive":true,"data":{"id":"module-other","serverUrl":"https://other.example.test/mcp"}}}}}}`))
		case "/saveTransactionsFanout":
			removalBody = body
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))

	result, err := RemoveMCPServer(&Account{
		UserID:        "user-1",
		SpaceID:       "space-1",
		SpaceViewID:   "view-1",
		TokenV2:       "token",
		ClientVersion: DefaultClientVersion,
	}, &MCPServerConfig{
		Name: "Workspace MCP",
		URL:  "https://mcp.example.test/server",
	})
	if err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	if !result.Removed || result.RemovedCount != 1 || result.ModuleID != "module-target" {
		t.Fatalf("unexpected remove result: %#v", result)
	}
	wantPaths := []string{"/loadUserContent", "/syncRecordValuesSpaceInitial", "/saveTransactionsFanout"}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("paths=%v want %v", paths, wantPaths)
	}

	transactions, _ := removalBody["transactions"].([]interface{})
	if len(transactions) != 1 {
		t.Fatalf("transactions=%#v", removalBody["transactions"])
	}
	transaction, _ := transactions[0].(map[string]interface{})
	debug, _ := transaction["debug"].(map[string]interface{})
	if debug["userAction"] != "ConnectionSurfaceTabs.disconnectPersonalMcpServer" {
		t.Fatalf("disconnect user action=%#v", debug)
	}
	operations, _ := transaction["operations"].([]interface{})
	if len(operations) != 2 {
		t.Fatalf("operations=%#v", transaction["operations"])
	}
	settingsOperation, _ := operations[0].(map[string]interface{})
	settings, _ := settingsOperation["args"].(map[string]interface{})
	if settingsOperation["command"] != "update" || !strings.Contains(string(mustJSON(t, settingsOperation["path"])), "settings") {
		t.Fatalf("settings operation=%#v", settingsOperation)
	}
	references, _ := settings["agent_chat_modules"].([]interface{})
	if len(references) != 1 || !strings.Contains(string(mustJSON(t, references[0])), "module-other") {
		t.Fatalf("filtered references=%#v", settings["agent_chat_modules"])
	}
	archiveOperation, _ := operations[1].(map[string]interface{})
	archivePointer, _ := archiveOperation["pointer"].(map[string]interface{})
	archiveArgs, _ := archiveOperation["args"].(map[string]interface{})
	if archivePointer["id"] != "module-target" || archiveOperation["command"] != "update" || archiveArgs["alive"] != false {
		t.Fatalf("archive operation=%#v", archiveOperation)
	}
}

func TestRemoveMCPServerIsNoOpWhenAlreadyAbsent(t *testing.T) {
	var paths []string
	withMCPNotionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPRequest(t, r)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/loadUserContent" {
			t.Fatalf("unexpected request for absent MCP: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"recordMap":{"space_view":{"view-1":{"value":{"value":{"id":"view-1","space_id":"space-1","settings":{"agent_chat_modules":[]}}}}}}}`))
	}))

	result, err := RemoveMCPServer(&Account{
		UserID:        "user-1",
		SpaceID:       "space-1",
		SpaceViewID:   "view-1",
		TokenV2:       "token",
		ClientVersion: DefaultClientVersion,
	}, &MCPServerConfig{Name: "Workspace MCP", URL: "https://mcp.example.test/server"})
	if err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	if result.Removed || result.RemovedCount != 0 || result.ModuleID != "" {
		t.Fatalf("unexpected no-op result: %#v", result)
	}
	if strings.Join(paths, ",") != "/loadUserContent" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestAccountBatchMCPRemovePreservesServerAcrossRetry(t *testing.T) {
	account := &Account{UserID: "user-1", UserEmail: "mcp-remove@example.test", SpaceID: "space-1"}
	account.EnsureAccountID()
	pool := NewAccountPool()
	pool.accounts = []*Account{account}
	store, err := NewMCPStore("")
	if err != nil {
		t.Fatal(err)
	}
	server, err := store.Upsert(MCPServerInput{
		Name:     "Workspace MCP",
		URL:      "https://mcp.example.test/server",
		AuthType: MCPAuthNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewAccountBatchManager(pool, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	manager.SetMCPStore(store)

	originalRemover := mcpRemover
	calls := 0
	mcpRemover = func(gotAccount *Account, gotServer *MCPServerConfig) (MCPRemoveResult, error) {
		calls++
		if gotAccount != account || gotServer.ID != server.ID {
			t.Fatalf("unexpected remover input: account=%#v server=%#v", gotAccount, gotServer)
		}
		if calls == 1 {
			return MCPRemoveResult{ModuleID: "module-1", RemovedCount: 1}, errors.New("transaction failed")
		}
		return MCPRemoveResult{ModuleID: "module-1", Removed: true, RemovedCount: 1}, nil
	}
	t.Cleanup(func() { mcpRemover = originalRemover })

	job, err := manager.StartMCPRemove(server.ID, []string{account.AccountID}, 1)
	if err != nil {
		t.Fatalf("StartMCPRemove: %v", err)
	}
	failed := waitForAccountBatchJob(t, manager, job.ID)
	if failed.MCPServerID != server.ID || failed.Failed != 1 || failed.Steps[0].ModuleID != "module-1" || failed.Steps[0].Removed == nil || *failed.Steps[0].Removed {
		t.Fatalf("unexpected failed remove job: %#v", failed)
	}

	retry, err := manager.Retry(failed.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	finished := waitForAccountBatchJob(t, manager, retry.ID)
	if finished.MCPServerID != server.ID || finished.Succeeded != 1 || finished.Steps[0].ModuleID != "module-1" || finished.Steps[0].Removed == nil || !*finished.Steps[0].Removed {
		t.Fatalf("unexpected retried remove job: %#v", finished)
	}
	if calls != 2 {
		t.Fatalf("remover calls=%d want 2", calls)
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

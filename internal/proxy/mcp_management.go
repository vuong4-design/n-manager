package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MCPAuthNone   = "none"
	MCPAuthBasic  = "basic"
	MCPAuthBearer = "bearer"
)

// MCPServerConfig is the private on-disk representation of one reusable MCP
// definition. Passwords/tokens are never returned by the admin API or copied
// into account batch-job history.
type MCPServerConfig struct {
	ID                         string    `json:"id"`
	Name                       string    `json:"name"`
	URL                        string    `json:"url"`
	AuthType                   string    `json:"auth_type"`
	Username                   string    `json:"username,omitempty"`
	Password                   string    `json:"password,omitempty"`
	BearerToken                string    `json:"bearer_token,omitempty"`
	RunWriteToolsAutomatically bool      `json:"run_write_tools_automatically"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type MCPServerPublic struct {
	ID                         string    `json:"id"`
	Name                       string    `json:"name"`
	URL                        string    `json:"url"`
	AuthType                   string    `json:"auth_type"`
	Username                   string    `json:"username,omitempty"`
	HasPassword                bool      `json:"has_password"`
	HasBearerToken             bool      `json:"has_bearer_token"`
	RunWriteToolsAutomatically bool      `json:"run_write_tools_automatically"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type MCPServerInput struct {
	ID                         string `json:"id,omitempty"`
	Name                       string `json:"name"`
	URL                        string `json:"url"`
	AuthType                   string `json:"auth_type"`
	Username                   string `json:"username,omitempty"`
	Password                   string `json:"password,omitempty"`
	BearerToken                string `json:"bearer_token,omitempty"`
	RunWriteToolsAutomatically bool   `json:"run_write_tools_automatically"`
}

type mcpStoreFile struct {
	Servers []*MCPServerConfig `json:"servers"`
}

type MCPStore struct {
	mu      sync.RWMutex
	path    string
	servers map[string]*MCPServerConfig
	order   []string
}

func NewMCPStore(path string) (*MCPStore, error) {
	store := &MCPStore{
		path:    strings.TrimSpace(path),
		servers: make(map[string]*MCPServerConfig),
	}
	if store.path == "" {
		return store, nil
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read MCP store: %w", err)
	}
	var persisted mcpStoreFile
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("parse MCP store: %w", err)
	}
	for _, server := range persisted.Servers {
		if server == nil {
			continue
		}
		server.ID = strings.TrimSpace(server.ID)
		if server.ID == "" {
			continue
		}
		server.AuthType = normalizeMCPAuthType(server.AuthType)
		server.URL = normalizeMCPServerURL(server.URL)
		clone := cloneMCPServerConfig(server)
		store.servers[server.ID] = clone
		store.order = append(store.order, server.ID)
	}
	return store, nil
}

func normalizeMCPAuthType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", MCPAuthNone:
		return MCPAuthNone
	case MCPAuthBasic:
		return MCPAuthBasic
	case MCPAuthBearer:
		return MCPAuthBearer
	default:
		return ""
	}
}

func normalizeMCPServerURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.Fragment = ""
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}
	return parsed.String()
}

func validateMCPServerInput(input MCPServerInput, existing *MCPServerConfig) (MCPServerConfig, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return MCPServerConfig{}, fmt.Errorf("MCP name is required")
	}
	serverURL := normalizeMCPServerURL(input.URL)
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return MCPServerConfig{}, fmt.Errorf("MCP URL must be an absolute HTTP or HTTPS URL")
	}
	authType := normalizeMCPAuthType(input.AuthType)
	if authType == "" {
		return MCPServerConfig{}, fmt.Errorf("unsupported MCP authentication type")
	}

	now := time.Now().UTC()
	server := MCPServerConfig{
		ID:                         strings.TrimSpace(input.ID),
		Name:                       name,
		URL:                        serverURL,
		AuthType:                   authType,
		Username:                   strings.TrimSpace(input.Username),
		Password:                   input.Password,
		BearerToken:                input.BearerToken,
		RunWriteToolsAutomatically: input.RunWriteToolsAutomatically,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	if existing != nil {
		server.ID = existing.ID
		server.CreatedAt = existing.CreatedAt
		if server.Password == "" {
			server.Password = existing.Password
		}
		if server.BearerToken == "" {
			server.BearerToken = existing.BearerToken
		}
	}
	if server.ID == "" {
		server.ID = generateUUIDv4()
	}

	switch authType {
	case MCPAuthNone:
		server.Username = ""
		server.Password = ""
		server.BearerToken = ""
	case MCPAuthBasic:
		server.BearerToken = ""
		if server.Username == "" {
			return MCPServerConfig{}, fmt.Errorf("Basic authentication username is required")
		}
		if server.Password == "" {
			return MCPServerConfig{}, fmt.Errorf("Basic authentication password is required")
		}
	case MCPAuthBearer:
		server.Username = ""
		server.Password = ""
		if server.BearerToken == "" {
			return MCPServerConfig{}, fmt.Errorf("Bearer token is required")
		}
	}
	return server, nil
}

func cloneMCPServerConfig(server *MCPServerConfig) *MCPServerConfig {
	if server == nil {
		return nil
	}
	clone := *server
	return &clone
}

func publicMCPServer(server *MCPServerConfig) MCPServerPublic {
	if server == nil {
		return MCPServerPublic{}
	}
	return MCPServerPublic{
		ID:                         server.ID,
		Name:                       server.Name,
		URL:                        server.URL,
		AuthType:                   server.AuthType,
		Username:                   server.Username,
		HasPassword:                server.Password != "",
		HasBearerToken:             server.BearerToken != "",
		RunWriteToolsAutomatically: server.RunWriteToolsAutomatically,
		CreatedAt:                  server.CreatedAt,
		UpdatedAt:                  server.UpdatedAt,
	}
}

func (s *MCPStore) persistLocked() error {
	if s == nil || s.path == "" {
		return nil
	}
	servers := make([]*MCPServerConfig, 0, len(s.order))
	for _, id := range s.order {
		if server := s.servers[id]; server != nil {
			servers = append(servers, cloneMCPServerConfig(server))
		}
	}
	data, err := json.MarshalIndent(mcpStoreFile{Servers: servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal MCP store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create MCP store directory: %w", err)
	}
	_ = os.Chmod(filepath.Dir(s.path), 0o700)
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".mcp-servers-*.tmp")
	if err != nil {
		return fmt.Errorf("create MCP store temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure MCP store temp file: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write MCP store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync MCP store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close MCP store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace MCP store: %w", err)
	}
	cleanup = false
	_ = os.Chmod(s.path, 0o600)
	return nil
}

func (s *MCPStore) List() []MCPServerPublic {
	if s == nil {
		return []MCPServerPublic{}
	}
	s.mu.RLock()
	result := make([]MCPServerPublic, 0, len(s.order))
	for _, id := range s.order {
		if server := s.servers[id]; server != nil {
			result = append(result, publicMCPServer(server))
		}
	}
	s.mu.RUnlock()
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func (s *MCPStore) Get(id string) (*MCPServerConfig, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	server := cloneMCPServerConfig(s.servers[strings.TrimSpace(id)])
	s.mu.RUnlock()
	return server, server != nil
}

func (s *MCPStore) Upsert(input MCPServerInput) (MCPServerPublic, error) {
	if s == nil {
		return MCPServerPublic{}, fmt.Errorf("MCP store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimSpace(input.ID)
	existing := s.servers[id]
	server, err := validateMCPServerInput(input, existing)
	if err != nil {
		return MCPServerPublic{}, err
	}
	if existing == nil {
		s.order = append(s.order, server.ID)
	}
	s.servers[server.ID] = cloneMCPServerConfig(&server)
	if err := s.persistLocked(); err != nil {
		if existing == nil {
			delete(s.servers, server.ID)
			s.order = s.order[:len(s.order)-1]
		} else {
			s.servers[server.ID] = existing
		}
		return MCPServerPublic{}, err
	}
	return publicMCPServer(&server), nil
}

func (s *MCPStore) Delete(id string) error {
	if s == nil {
		return fmt.Errorf("MCP store is not initialized")
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	server := s.servers[id]
	if server == nil {
		return os.ErrNotExist
	}
	oldOrder := append([]string(nil), s.order...)
	delete(s.servers, id)
	for index, candidate := range s.order {
		if candidate == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if err := s.persistLocked(); err != nil {
		s.servers[id] = server
		s.order = oldOrder
		return err
	}
	return nil
}

// MCPTool is the normalized tool metadata persisted by Notion's mcpServer
// workflow module. InputSchema is accepted from validation but intentionally
// omitted when writing the workflow module, matching the official client.
type MCPTool struct {
	Name        string             `json:"name"`
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description,omitempty"`
	Annotations MCPToolAnnotations `json:"annotations,omitempty"`
	InputSchema json.RawMessage    `json:"inputSchema,omitempty"`
}

type MCPToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
	DestructiveHint bool `json:"destructiveHint"`
}

type mcpValidationResponse struct {
	Success      bool      `json:"success"`
	OfficialName string    `json:"officialName"`
	Tools        []MCPTool `json:"tools"`
	Icon         string    `json:"icon"`
}

type mcpAuthHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func buildMCPAuthHeaders(server *MCPServerConfig) ([]mcpAuthHeader, error) {
	if server == nil {
		return nil, fmt.Errorf("nil MCP server")
	}
	switch server.AuthType {
	case MCPAuthNone:
		return []mcpAuthHeader{}, nil
	case MCPAuthBasic:
		value := base64.StdEncoding.EncodeToString([]byte(server.Username + ":" + server.Password))
		return []mcpAuthHeader{{Name: "Authorization", Value: "Basic " + value}}, nil
	case MCPAuthBearer:
		return []mcpAuthHeader{{Name: "Authorization", Value: "Bearer " + server.BearerToken}}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP authentication type")
	}
}

func postNotionMCPJSON(acc *Account, endpoint string, payload interface{}, output interface{}) error {
	if acc == nil {
		return fmt.Errorf("nil account")
	}
	if AppConfig == nil {
		return fmt.Errorf("application config is not initialized")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", endpoint, err)
	}
	req, err := http.NewRequest(http.MethodPost, NotionAPIBase+"/"+strings.TrimPrefix(endpoint, "/"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", endpoint, err)
	}
	setNotionHeadersJSON(req, acc)
	client := getChromeHTTPClient(AppConfig.APITimeoutDuration())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send %s request: %w", endpoint, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("read %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiError struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &apiError)
		detail := strings.TrimSpace(apiError.Name)
		if detail == "" {
			detail = strings.TrimSpace(apiError.Error)
		}
		if detail != "" {
			return fmt.Errorf("%s API error %d: %s", endpoint, resp.StatusCode, truncateForLog(detail, 120))
		}
		return fmt.Errorf("%s API error %d", endpoint, resp.StatusCode)
	}
	if output == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("parse %s response: %w", endpoint, err)
	}
	return nil
}

func validateNotionMCPServer(acc *Account, server *MCPServerConfig, authHeaders []mcpAuthHeader) (mcpValidationResponse, error) {
	var validation mcpValidationResponse
	err := postNotionMCPJSON(acc, "validateMcpConnection", map[string]interface{}{
		"serverUrl":   server.URL,
		"spaceId":     acc.SpaceID,
		"authHeaders": authHeaders,
	}, &validation)
	if err != nil {
		return mcpValidationResponse{}, err
	}
	if len(validation.Tools) == 0 {
		return mcpValidationResponse{}, fmt.Errorf("MCP server returned no tools")
	}
	if strings.TrimSpace(validation.Icon) == "" {
		validation.Icon = "🤖"
	}
	return validation, nil
}

type notionMCPPointer struct {
	Table   string `json:"table"`
	ID      string `json:"id"`
	SpaceID string `json:"spaceId"`
}

type notionMCPModuleReference struct {
	Pointer        notionMCPPointer `json:"pointer"`
	ID             string           `json:"id,omitempty"`
	Type           string           `json:"type,omitempty"`
	DefaultEnabled bool             `json:"defaultEnabled"`
}

type notionMCPSpaceViewValue struct {
	ID       string                 `json:"id"`
	SpaceID  string                 `json:"space_id"`
	Settings map[string]interface{} `json:"settings"`
}

type notionMCPSpaceViewRecord struct {
	Value struct {
		Value *notionMCPSpaceViewValue `json:"value"`
		notionMCPSpaceViewValue
	} `json:"value"`
}

func (record notionMCPSpaceViewRecord) value() notionMCPSpaceViewValue {
	if record.Value.Value != nil {
		return *record.Value.Value
	}
	return record.Value.notionMCPSpaceViewValue
}

type mcpWorkspaceState struct {
	SpaceViewID       string
	Settings          map[string]interface{}
	ModuleIDs         []string
	AssignedModuleIDs map[string]bool
}

func cloneMCPSettings(settings map[string]interface{}) map[string]interface{} {
	if settings == nil {
		return map[string]interface{}{}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return map[string]interface{}{}
	}
	var clone map[string]interface{}
	if err := json.Unmarshal(data, &clone); err != nil || clone == nil {
		return map[string]interface{}{}
	}
	return clone
}

func mcpModuleReferencesFromSettings(settings map[string]interface{}) []notionMCPModuleReference {
	raw, ok := settings["agent_chat_modules"]
	if !ok || raw == nil {
		return []notionMCPModuleReference{}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return []notionMCPModuleReference{}
	}
	var references []notionMCPModuleReference
	if err := json.Unmarshal(data, &references); err != nil {
		return []notionMCPModuleReference{}
	}
	return references
}

func loadMCPWorkspaceState(acc *Account) (mcpWorkspaceState, error) {
	var response struct {
		RecordMap struct {
			SpaceView map[string]json.RawMessage `json:"space_view"`
		} `json:"recordMap"`
	}
	if err := postNotionMCPJSON(acc, "loadUserContent", map[string]interface{}{}, &response); err != nil {
		return mcpWorkspaceState{}, err
	}
	state := mcpWorkspaceState{AssignedModuleIDs: make(map[string]bool)}
	for id, raw := range response.RecordMap.SpaceView {
		var record notionMCPSpaceViewRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			continue
		}
		value := record.value()
		spaceViewID := strings.TrimSpace(value.ID)
		if spaceViewID == "" {
			spaceViewID = id
		}
		if acc.SpaceViewID != "" && spaceViewID != acc.SpaceViewID && id != acc.SpaceViewID {
			continue
		}
		if acc.SpaceViewID == "" && value.SpaceID != "" && value.SpaceID != acc.SpaceID {
			continue
		}
		state.SpaceViewID = spaceViewID
		state.Settings = cloneMCPSettings(value.Settings)
		for _, module := range mcpModuleReferencesFromSettings(value.Settings) {
			moduleID := strings.TrimSpace(module.Pointer.ID)
			if moduleID == "" {
				moduleID = strings.TrimSpace(module.ID)
			}
			if moduleID == "" {
				continue
			}
			state.ModuleIDs = append(state.ModuleIDs, moduleID)
			state.AssignedModuleIDs[moduleID] = true
		}
		break
	}
	if state.SpaceViewID == "" {
		return mcpWorkspaceState{}, fmt.Errorf("selected space view was not found")
	}
	return state, nil
}

type notionMCPModuleData struct {
	ID                         string    `json:"id"`
	Icon                       string    `json:"icon"`
	Name                       string    `json:"name"`
	Tools                      []MCPTool `json:"tools"`
	ServerURL                  string    `json:"serverUrl"`
	RunWriteToolsAutomatically bool      `json:"runWriteToolsAutomatically"`
}

type notionMCPWorkflowModuleValue struct {
	ID         string              `json:"id"`
	SpaceID    string              `json:"space_id"`
	ModuleType string              `json:"module_type"`
	Data       notionMCPModuleData `json:"data"`
	Version    int                 `json:"version"`
}

type notionMCPWorkflowModuleRecord struct {
	Value struct {
		Value *notionMCPWorkflowModuleValue `json:"value"`
		notionMCPWorkflowModuleValue
	} `json:"value"`
}

func (record notionMCPWorkflowModuleRecord) value() notionMCPWorkflowModuleValue {
	if record.Value.Value != nil {
		return *record.Value.Value
	}
	return record.Value.notionMCPWorkflowModuleValue
}

func loadNotionMCPModules(acc *Account, moduleIDs []string) (map[string]notionMCPWorkflowModuleValue, error) {
	result := make(map[string]notionMCPWorkflowModuleValue)
	if len(moduleIDs) == 0 {
		return result, nil
	}
	requests := make([]map[string]interface{}, 0, len(moduleIDs))
	for _, moduleID := range moduleIDs {
		requests = append(requests, map[string]interface{}{
			"pointer": notionMCPPointer{Table: "workflow_module", ID: moduleID, SpaceID: acc.SpaceID},
			"version": -1,
		})
	}
	var response struct {
		RecordMap struct {
			WorkflowModule map[string]json.RawMessage `json:"workflow_module"`
		} `json:"recordMap"`
		WorkflowModule map[string]json.RawMessage `json:"workflow_module"`
	}
	if err := postNotionMCPJSON(acc, "syncRecordValuesSpaceInitial", map[string]interface{}{
		"requests": requests,
		"spacePointer": map[string]string{
			"table": "space",
			"id":    acc.SpaceID,
		},
	}, &response); err != nil {
		return nil, err
	}
	records := response.RecordMap.WorkflowModule
	if len(records) == 0 {
		records = response.WorkflowModule
	}
	for id, raw := range records {
		var record notionMCPWorkflowModuleRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			continue
		}
		value := record.value()
		if value.ID == "" {
			value.ID = id
		}
		result[value.ID] = value
	}
	return result, nil
}

type notionMCPDebug struct {
	UserAction         string `json:"userAction"`
	ClientCommitTimeMs int64  `json:"clientCommitTimeMs"`
}

type notionMCPOperation struct {
	Pointer notionMCPPointer `json:"pointer"`
	Path    []string         `json:"path"`
	Command string           `json:"command"`
	Args    interface{}      `json:"args"`
}

type notionMCPTransaction struct {
	ID         string               `json:"id"`
	SpaceID    string               `json:"spaceId"`
	Debug      notionMCPDebug       `json:"debug"`
	Operations []notionMCPOperation `json:"operations"`
}

func saveNotionMCPTransaction(acc *Account, endpoint, userAction string, operations []notionMCPOperation) error {
	return postNotionMCPJSON(acc, endpoint, map[string]interface{}{
		"requestId": generateUUIDv4(),
		"transactions": []notionMCPTransaction{{
			ID:      generateUUIDv4(),
			SpaceID: acc.SpaceID,
			Debug: notionMCPDebug{
				UserAction:         userAction,
				ClientCommitTimeMs: time.Now().UnixMilli(),
			},
			Operations: operations,
		}},
	}, nil)
}

func persistedMCPTools(tools []MCPTool) []MCPTool {
	result := make([]MCPTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, MCPTool{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			Annotations: tool.Annotations,
		})
	}
	return result
}

type MCPInstallResult struct {
	ModuleID                   string `json:"module_id"`
	Created                    bool   `json:"created"`
	AssignedToNotionAgent      bool   `json:"assigned_to_notion_agent"`
	Connected                  bool   `json:"connected"`
	WriteToolsRunAutomatically bool   `json:"write_tools_run_automatically"`
}

type MCPPartialInstallError struct {
	Result MCPInstallResult
	Err    error
}

func (e *MCPPartialInstallError) Error() string {
	if e == nil || e.Err == nil {
		return "MCP installation incomplete"
	}
	return e.Err.Error()
}

func (e *MCPPartialInstallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var mcpInstaller = InstallMCPServer

// InstallMCPServer reproduces Notion's Custom MCP setup flow using the account
// cookie directly: validate, create/reuse module, attach to Notion Agent,
// connect credentials, and set write-tool execution policy.
func InstallMCPServer(acc *Account, server *MCPServerConfig) (MCPInstallResult, error) {
	if acc == nil || server == nil {
		return MCPInstallResult{}, fmt.Errorf("account and MCP server are required")
	}
	authHeaders, err := buildMCPAuthHeaders(server)
	if err != nil {
		return MCPInstallResult{}, err
	}
	validation, err := validateNotionMCPServer(acc, server, authHeaders)
	if err != nil {
		return MCPInstallResult{}, err
	}
	tools := persistedMCPTools(validation.Tools)
	moduleData := notionMCPModuleData{
		Icon:                       validation.Icon,
		Name:                       server.Name,
		Tools:                      tools,
		ServerURL:                  server.URL,
		RunWriteToolsAutomatically: server.RunWriteToolsAutomatically,
	}

	workspace, err := loadMCPWorkspaceState(acc)
	if err != nil {
		return MCPInstallResult{}, err
	}
	modules, err := loadNotionMCPModules(acc, workspace.ModuleIDs)
	if err != nil {
		return MCPInstallResult{}, err
	}
	moduleID := ""
	for id, module := range modules {
		if normalizeMCPServerURL(module.Data.ServerURL) == server.URL {
			moduleID = id
			break
		}
	}

	result := MCPInstallResult{WriteToolsRunAutomatically: server.RunWriteToolsAutomatically}
	if moduleID == "" {
		moduleID = generateUUIDv4()
		result.Created = true
		moduleData.ID = moduleID
		nowMs := time.Now().UnixMilli()
		creationData := map[string]interface{}{
			"serverUrl": server.URL,
			"tools":     tools,
			"icon":      validation.Icon,
			"id":        moduleID,
			"name":      server.Name,
		}
		moduleValue := map[string]interface{}{
			"alive":                true,
			"created_by_id":        acc.UserID,
			"created_by_table":     "notion_user",
			"created_time":         nowMs,
			"id":                   moduleID,
			"last_edited_by_id":    acc.UserID,
			"last_edited_by_table": "notion_user",
			"last_edited_time":     nowMs,
			"parent_id":            acc.UserID,
			"parent_table":         "notion_user",
			"version":              1,
			"module_type":          "mcpServer",
			"space_id":             acc.SpaceID,
			"data":                 creationData,
		}
		if err := saveNotionMCPTransaction(acc, "saveTransactionsFanout", "agentPersistenceHelpers.createAgentChatModule", []notionMCPOperation{{
			Pointer: notionMCPPointer{Table: "workflow_module", ID: moduleID, SpaceID: acc.SpaceID},
			Path:    []string{},
			Command: "set",
			Args:    moduleValue,
		}}); err != nil {
			return MCPInstallResult{}, err
		}
	} else {
		moduleData.ID = moduleID
	}
	result.ModuleID = moduleID

	connectErr := postNotionMCPJSON(acc, "postWorkflowsMcpServerConnect", map[string]interface{}{
		"integrationId":     moduleID,
		"spaceId":           acc.SpaceID,
		"authHeaders":       authHeaders,
		"initiationContext": "connect",
	}, nil)
	result.Connected = connectErr == nil

	if !workspace.AssignedModuleIDs[moduleID] {
		settings := cloneMCPSettings(workspace.Settings)
		references := mcpModuleReferencesFromSettings(settings)
		references = append(references, notionMCPModuleReference{
			Pointer:        notionMCPPointer{Table: "workflow_module", ID: moduleID, SpaceID: acc.SpaceID},
			DefaultEnabled: false,
		})
		settings["agent_chat_modules"] = references
		if err := saveNotionMCPTransaction(acc, "saveTransactionsFanout", "agentPersistenceHelpers.addAgentChatModule", []notionMCPOperation{{
			Pointer: notionMCPPointer{Table: "space_view", ID: workspace.SpaceViewID, SpaceID: acc.SpaceID},
			Path:    []string{"settings"},
			Command: "update",
			Args:    settings,
		}}); err != nil {
			if connectErr != nil {
				return result, &MCPPartialInstallError{Result: result, Err: errors.Join(connectErr, err)}
			}
			return result, &MCPPartialInstallError{Result: result, Err: err}
		}
	}
	result.AssignedToNotionAgent = true

	updateErr := saveNotionMCPTransaction(acc, "saveTransactionsFanout", "ConnectionSurfaceTabs.updateMcpToolPermissions", []notionMCPOperation{{
		Pointer: notionMCPPointer{Table: "workflow_module", ID: moduleID, SpaceID: acc.SpaceID},
		Path:    []string{"data"},
		Command: "update",
		Args:    moduleData,
	}})
	if updateErr != nil {
		if connectErr != nil {
			return result, &MCPPartialInstallError{Result: result, Err: errors.Join(connectErr, updateErr)}
		}
		return result, &MCPPartialInstallError{Result: result, Err: updateErr}
	}
	if connectErr != nil {
		return result, &MCPPartialInstallError{Result: result, Err: connectErr}
	}
	return result, nil
}

func HandleAdminMCPServers(store *MCPStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !authorizeAccountBatch(auth, w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"servers": store.List()})
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			var input MCPServerInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
				return
			}
			created, err := store.Upsert(input)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(created)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}
}

func HandleAdminMCPServerRouter(store *MCPStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !authorizeAccountBatch(auth, w, r) {
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/mcp-servers/"), "/")
		if id == "" || strings.Contains(id, "/") {
			http.Error(w, `{"error":"MCP server id is required"}`, http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			server, ok := store.Get(id)
			if !ok {
				http.Error(w, `{"error":"MCP server not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(publicMCPServer(server))
		case http.MethodPut:
			if _, ok := store.Get(id); !ok {
				http.Error(w, `{"error":"MCP server not found"}`, http.StatusNotFound)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			var input MCPServerInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
				return
			}
			input.ID = id
			updated, err := store.Upsert(input)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(updated)
		case http.MethodDelete:
			if err := store.Delete(id); err != nil {
				if os.IsNotExist(err) {
					http.Error(w, `{"error":"MCP server not found"}`, http.StatusNotFound)
					return
				}
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}
}

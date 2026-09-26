package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const CurrentVersion = 1

type Config struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`
	Callers     []Caller     `json:"callers"`
}

type Connection struct {
	Name               string      `json:"name"`
	ProjectID          string      `json:"project_id"`
	Location           string      `json:"location"`
	CredentialsFileEnv string      `json:"credentials_file_env"`
	Datasets           []string    `json:"datasets"`
	Resources          []Resource  `json:"resources"`
	Limits             QueryLimits `json:"limits"`
}

type Resource struct {
	Dataset        string   `json:"dataset"`
	Table          string   `json:"table"`
	EnabledColumns []string `json:"enabled_columns"`
}

type QueryLimits struct {
	MaxBytesBilled   int64 `json:"max_bytes_billed"`
	MaxPageRows      int   `json:"max_page_rows"`
	MaxResponseBytes int64 `json:"max_response_bytes"`
}

type Caller struct {
	Name               string   `json:"name"`
	TokenEnv           string   `json:"token_env"`
	AllowedConnections []string `json:"allowed_connections"`
}

type LookupEnv func(string) (string, bool)

func Load(path string, lookup LookupEnv) (Config, error) {
	cfg, err := Read(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(lookup); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Read strictly decodes a config file without resolving runtime secrets. It is
// used by admin views that need to display references and status, never values.
func Read(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("configuration must contain exactly one JSON value")
		}
		return Config{}, fmt.Errorf("read trailing configuration data: %w", err)
	}
	return cfg, nil
}

// WriteAtomic writes a complete config snapshot beside the destination and
// renames it into place so readers never observe a partial JSON file.
func WriteAtomic(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect existing config: %w", err)
	}

	file, err := os.CreateTemp(dir, ".bqrest-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		defer dirFile.Close()
		if err := dirFile.Sync(); err != nil {
			return fmt.Errorf("sync config directory: %w", err)
		}
	}
	return nil
}

func (c Config) Validate(lookup LookupEnv) error {
	if err := c.ValidateStructure(); err != nil {
		return err
	}
	if lookup == nil {
		return errors.New("environment lookup is required")
	}
	for _, connection := range c.Connections {
		credentialsPath, ok := lookup(connection.CredentialsFileEnv)
		if !ok || strings.TrimSpace(credentialsPath) == "" {
			return fmt.Errorf("connection %q: environment variable %s must point to a mounted credentials file", connection.Name, connection.CredentialsFileEnv)
		}
		info, err := os.Stat(credentialsPath)
		if err != nil {
			return fmt.Errorf("connection %q: credentials file from %s: %w", connection.Name, connection.CredentialsFileEnv, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("connection %q: credentials path from %s must be a regular file", connection.Name, connection.CredentialsFileEnv)
		}
	}
	for _, caller := range c.Callers {
		if token, ok := lookup(caller.TokenEnv); !ok || strings.TrimSpace(token) == "" {
			return fmt.Errorf("caller %q: environment variable %s must contain its API token", caller.Name, caller.TokenEnv)
		}
	}
	return nil
}

func (c Config) ValidateStructure() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d (want %d)", c.Version, CurrentVersion)
	}
	if len(c.Connections) == 0 {
		return errors.New("at least one BigQuery connection is required")
	}
	connections := make(map[string]struct{}, len(c.Connections))
	for i, connection := range c.Connections {
		if !validName(connection.Name) {
			return fmt.Errorf("connections[%d].name is required and may contain only letters, digits, hyphens, and underscores", i)
		}
		if _, exists := connections[connection.Name]; exists {
			return fmt.Errorf("duplicate connection name %q", connection.Name)
		}
		connections[connection.Name] = struct{}{}
		if strings.TrimSpace(connection.ProjectID) == "" {
			return fmt.Errorf("connection %q: project_id is required", connection.Name)
		}
		if strings.TrimSpace(connection.Location) == "" {
			return fmt.Errorf("connection %q: location is required", connection.Name)
		}
		if !validEnvName(connection.CredentialsFileEnv) {
			return fmt.Errorf("connection %q: credentials_file_env must name an environment variable", connection.Name)
		}
		if len(connection.Datasets) == 0 {
			return fmt.Errorf("connection %q: at least one configured dataset is required", connection.Name)
		}
		datasets := make(map[string]struct{}, len(connection.Datasets))
		for _, dataset := range connection.Datasets {
			if !validName(dataset) {
				return fmt.Errorf("connection %q: invalid dataset name %q", connection.Name, dataset)
			}
			if _, exists := datasets[dataset]; exists {
				return fmt.Errorf("connection %q: duplicate dataset %q", connection.Name, dataset)
			}
			datasets[dataset] = struct{}{}
		}
		resourceNames := make(map[string]struct{}, len(connection.Resources))
		for j, resource := range connection.Resources {
			if !validName(resource.Dataset) || !validName(resource.Table) {
				return fmt.Errorf("connection %q resources[%d]: dataset and table names may contain only letters, digits, hyphens, and underscores", connection.Name, j)
			}
			if _, ok := datasets[resource.Dataset]; !ok {
				return fmt.Errorf("connection %q resource %s.%s is outside configured datasets", connection.Name, resource.Dataset, resource.Table)
			}
			key := resource.Dataset + "." + resource.Table
			if _, exists := resourceNames[key]; exists {
				return fmt.Errorf("connection %q: duplicate resource %q", connection.Name, key)
			}
			resourceNames[key] = struct{}{}
			if len(resource.EnabledColumns) == 0 {
				return fmt.Errorf("connection %q resource %q: at least one enabled column is required", connection.Name, key)
			}
			columns := make(map[string]struct{}, len(resource.EnabledColumns))
			for _, column := range resource.EnabledColumns {
				if !validColumnName(column) {
					return fmt.Errorf("connection %q resource %q: invalid enabled column %q", connection.Name, key, column)
				}
				if _, exists := columns[column]; exists {
					return fmt.Errorf("connection %q resource %q: duplicate enabled column %q", connection.Name, key, column)
				}
				columns[column] = struct{}{}
			}
		}
		if connection.Limits.MaxBytesBilled <= 0 || connection.Limits.MaxPageRows <= 0 || connection.Limits.MaxResponseBytes <= 0 {
			return fmt.Errorf("connection %q: max_bytes_billed, max_page_rows, and max_response_bytes must all be positive", connection.Name)
		}
	}

	if len(c.Callers) == 0 {
		return errors.New("at least one caller token is required")
	}
	callerNames := make(map[string]struct{}, len(c.Callers))
	tokenEnvs := make(map[string]struct{}, len(c.Callers))
	for i, caller := range c.Callers {
		if !validName(caller.Name) {
			return fmt.Errorf("callers[%d].name is required and may contain only letters, digits, hyphens, and underscores", i)
		}
		if _, exists := callerNames[caller.Name]; exists {
			return fmt.Errorf("duplicate caller name %q", caller.Name)
		}
		callerNames[caller.Name] = struct{}{}
		if !validEnvName(caller.TokenEnv) {
			return fmt.Errorf("caller %q: token_env must name an environment variable", caller.Name)
		}
		if _, exists := tokenEnvs[caller.TokenEnv]; exists {
			return fmt.Errorf("caller %q: token_env %s is already assigned to another caller", caller.Name, caller.TokenEnv)
		}
		tokenEnvs[caller.TokenEnv] = struct{}{}
		if len(caller.AllowedConnections) == 0 {
			return fmt.Errorf("caller %q: at least one allowed connection is required", caller.Name)
		}
		granted := make(map[string]struct{}, len(caller.AllowedConnections))
		for _, name := range caller.AllowedConnections {
			if _, ok := connections[name]; !ok {
				return fmt.Errorf("caller %q: allowed connection %q is not configured", caller.Name, name)
			}
			if _, duplicate := granted[name]; duplicate {
				return fmt.Errorf("caller %q: duplicate connection grant %q", caller.Name, name)
			}
			granted[name] = struct{}{}
		}
	}
	return nil
}

func validName(value string) bool {
	if value == "" || len(value) > 1024 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// BigQuery permits flexible field names that are broader than config keys and
// dataset IDs. Preserve names returned by the BigQuery schema API while
// rejecting empty, oversized, and control-character values.
func validColumnName(value string) bool {
	if value == "" || len([]rune(value)) > 300 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

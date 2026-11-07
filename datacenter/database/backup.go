package database

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alikatgh/le-cli/internal/boundedexec"
	"go.yaml.in/yaml/v3"
)

var ErrBackup = errors.New("database backup failed; any newly created partial file is preserved")

type BackupOptions struct {
	Destination     string
	Binary          string
	CredentialsFile string
	MaxBytes        int64
	Timeout         time.Duration
}
type BackupResult struct {
	Engine    string    `json:"engine"`
	DataMode  string    `json:"data_mode"`
	Database  string    `json:"database"`
	CreatedAt time.Time `json:"created_at"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	Complete  bool      `json:"complete"`
	Format    string    `json:"format"`
	Notice    string    `json:"notice"`
}

// Backup uses the vendor's dump format. It creates only a new private file,
// never overwrites/removes a destination, and preserves partial output on error.
func Backup(ctx context.Context, c Config, o BackupOptions) (BackupResult, error) {
	if runtime.GOOS == "windows" {
		return BackupResult{}, errors.New("vendor backups are currently supported on macOS and Linux; Windows process-tree containment is not yet available")
	}
	if ctx == nil || ValidateConfig(c) != nil || !filepath.IsAbs(o.Destination) || o.MaxBytes <= 0 || o.MaxBytes > 256<<20 || o.Timeout <= 0 || o.Timeout > 5*time.Minute {
		return BackupResult{}, ErrConfig
	}
	name, args, environment, format, err := backupCommand(c, o)
	if err != nil {
		return BackupResult{}, err
	}
	if o.Binary != "" {
		if !filepath.IsAbs(o.Binary) {
			return BackupResult{}, ErrConfig
		}
		name = o.Binary
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return BackupResult{}, errors.New("required vendor dump utility is unavailable; install it independently")
	}
	file, err := createPrivateBackup(o.Destination)
	if err != nil {
		return BackupResult{}, errors.New("cannot exclusively create a private backup; existing files are preserved")
	}
	result := BackupResult{Engine: c.Engine, DataMode: "live", Database: c.Database, CreatedAt: time.Now().UTC(), Path: o.Destination, Format: format, Notice: "Native vendor format; restore compatibility and consistency must be verified separately. Contains potentially sensitive database data. Process deadlines do not bound OS-uninterruptible filesystem I/O."}
	if c.Engine == "redis" {
		result.Database = "all logical databases"
		result.Notice += " Redis replication snapshots include the whole instance and may trigger BGSAVE; --database does not scope the backup."
	}
	childCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	result.Bytes, err = boundedexec.Stream(childCtx, path, args, environment, file, o.MaxBytes)
	if err == nil && result.Bytes == 0 {
		err = ErrBackup
	}
	if err == nil {
		if syncErr := file.Sync(); syncErr != nil {
			err = ErrBackup
		}
	}
	if closeErr := file.Close(); err == nil && closeErr != nil {
		err = ErrBackup
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, boundedexec.ErrOutputLimit) {
			return result, err
		}
		return result, ErrBackup
	}
	result.Complete = true
	return result, nil
}

func backupCommand(c Config, o BackupOptions) (string, []string, []string, string, error) {
	host, port, _ := net.SplitHostPort(c.Address)
	tlsEnabled := c.TLS || c.CAFile != "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()
	env := []string{"LC_ALL=C"}
	for _, key := range []string{"PATH", "HOME", "SYSTEMROOT", "WINDIR", "APPDATA", "TEMP", "TMP", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	switch c.Engine {
	case "postgres":
		if strings.ContainsAny(c.Database, "=/\\?") {
			return "", nil, nil, "", ErrConfig
		}
		env = append(env, "PGHOST="+host, "PGPORT="+port, "PGDATABASE="+c.Database, "PGUSER="+c.Username, "PGPASSWORD="+c.Password, "PGPASSFILE="+os.DevNull, "PGSERVICEFILE="+os.DevNull, "PGCONNECT_TIMEOUT=5", "PGOPTIONS=-c statement_timeout=60000 -c default_transaction_read_only=on")
		if tlsEnabled {
			env = append(env, "PGSSLMODE=verify-full")
		} else {
			env = append(env, "PGSSLMODE=disable")
		}
		if c.CAFile != "" {
			env = append(env, "PGSSLROOTCERT="+c.CAFile)
		}
		return "pg_dump", []string{"--format=custom", "--no-owner", "--no-privileges", "--lock-wait-timeout=5s"}, env, "postgres-custom", nil
	case "mysql", "mariadb":
		if err := validateVendorConfig(o.CredentialsFile, c); err != nil {
			return "", nil, nil, "", err
		}
		args := []string{"--defaults-file=" + o.CredentialsFile, "--protocol=TCP", "--host=" + host, "--port=" + port, "--single-transaction", "--quick", "--skip-lock-tables", "--databases"}
		name := "mysqldump"
		if c.Engine == "mariadb" {
			name = "mariadb-dump"
			if tlsEnabled {
				args = append(args, "--ssl", "--ssl-verify-server-cert")
			}
		} else {
			if tlsEnabled {
				args = append(args, "--ssl-mode=VERIFY_IDENTITY")
			} else {
				args = append(args, "--ssl-mode=DISABLED")
			}
		}
		if c.CAFile != "" {
			args = append(args, "--ssl-ca="+c.CAFile)
		}
		args = append(args, "--", c.Database)
		return name, args, env, "sql", nil
	case "mongodb":
		if err := validateVendorConfig(o.CredentialsFile, c); err != nil {
			return "", nil, nil, "", err
		}
		args := []string{"--config=" + o.CredentialsFile, "--archive", "--quiet", "--numParallelCollections=1", "--db=" + c.Database}
		if tlsEnabled {
			args = append(args, "--ssl")
		}
		if c.CAFile != "" {
			args = append(args, "--sslCAFile="+c.CAFile)
		}
		return "mongodump", args, env, "mongodb-archive", nil
	case "redis":
		args := []string{"-h", host, "-p", port, "-n", c.Database, "-t", "5", "--rdb", "-"}
		if c.Username != "" {
			args = append(args, "--user", c.Username)
		}
		if tlsEnabled {
			args = append(args, "--tls", "--sni", host, "--tls-expected-peer-name", host)
		}
		if c.CAFile != "" {
			args = append(args, "--cacert", c.CAFile)
		}
		env = append(env, "REDISCLI_AUTH="+c.Password)
		return "redis-cli", args, env, "redis-rdb (replication snapshot; may trigger server BGSAVE)", nil
	}
	return "", nil, nil, "", ErrConfig
}

// Only existing protected vendor configuration is accepted. Mongo URIs must
// select exactly the requested direct endpoint; MySQL profiles cannot include
// init commands, includes, output paths or arbitrary client options.
func validateVendorConfig(path string, c Config) error {
	if !filepath.IsAbs(path) {
		return errors.New("this backup requires an existing protected vendor credentials file")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return ErrConfig
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("vendor credentials file must be private to its owner")
	}
	// #nosec G304 -- Explicit operator-selected existing regular owner-private vendor profile; no discovery.
	file, err := os.Open(path)
	if err != nil {
		return ErrConfig
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return ErrConfig
	}
	if c.Engine == "mongodb" {
		var values map[string]string
		if yaml.Unmarshal(data, &values) != nil {
			return ErrConfig
		}
		for key := range values {
			if key != "uri" && key != "password" && key != "sslPEMKeyPassword" {
				return ErrConfig
			}
		}
		u, err := url.Parse(values["uri"])
		if err != nil || u.Scheme != "mongodb" || u.Host != c.Address || u.Fragment != "" || u.Path != "" && u.Path != "/"+c.Database {
			return ErrConfig
		}
		q, parseErr := url.ParseQuery(u.RawQuery)
		if parseErr != nil {
			return ErrConfig
		}
		for _, values := range q {
			if len(values) != 1 {
				return ErrConfig
			}
		}
		if q.Get("directConnection") != "true" {
			return errors.New("mongodump config must use the selected endpoint with directConnection=true")
		}
		for key := range q {
			switch key {
			case "directConnection", "authSource", "authMechanism", "tls":
			default:
				return ErrConfig
			}
		}
		if q.Get("authMechanism") != "" && q.Get("authMechanism") != "SCRAM-SHA-256" && q.Get("authMechanism") != "SCRAM-SHA-1" {
			return ErrConfig
		}
		if q.Get("tls") != "" && q.Get("tls") != "true" {
			return ErrConfig
		}
		return nil
	}
	allowed := map[string]bool{"user": true, "password": true, "host": true, "port": true, "default-character-set": true, "ssl-ca": true, "ssl-cert": true, "ssl-key": true}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line != "[client]" && line != "[mysqldump]" && line != "[mariadb-dump]" {
				return ErrConfig
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || !allowed[strings.ToLower(strings.TrimSpace(parts[0]))] {
			return ErrConfig
		}
	}
	if scanner.Err() != nil {
		return ErrConfig
	}
	return nil
}

// Backup defaults are kept in one place for CLI and fixture verification.
func DefaultBackupOptions(destination string) BackupOptions {
	return BackupOptions{Destination: destination, MaxBytes: 64 << 20, Timeout: 60 * time.Second}
}

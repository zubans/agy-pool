// Package config читает настройки сервиса из переменных окружения.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/zubans/agy-pool/internal/logging"
)

// SecretsKeySize — длина ключа шифрования секретов (AES-256).
const SecretsKeySize = 32

// Config — настройки сервиса. Длительности в окружении задаются целым числом секунд.
type Config struct {
	// Ожидание ответа модели и запуски agy.
	TimeoutMax  time.Duration `env:"AGY_TIMEOUT_MAX" envDefault:"600"`
	Concurrency int           `env:"AGY_CONCURRENCY" envDefault:"2"`

	// Остывание учётных данных.
	QuotaCooldown    time.Duration `env:"AGY_QUOTA_COOLDOWN" envDefault:"3600"`
	AuthCooldown     time.Duration `env:"AGY_AUTH_COOLDOWN" envDefault:"600"`
	RegionCooldown   time.Duration `env:"AGY_REGION_COOLDOWN" envDefault:"86400"`
	EndpointCooldown time.Duration `env:"AGY_ENDPOINT_COOLDOWN" envDefault:"300"`
	PreferPrimary    bool          `env:"AGY_PREFER_PRIMARY" envDefault:"false"`

	// Журнал запросов.
	LogDays int `env:"AGY_LOG_DAYS" envDefault:"90"`

	// Вложения.
	AttachMaxFiles int      `env:"AGY_ATTACH_MAX_FILES" envDefault:"10"`
	AttachMaxBytes ByteSize `env:"AGY_ATTACH_MAX_BYTES" envDefault:"209715200"`

	// Ключ шифрования API-ключей в БД: base64 от 32 байт. Пока необязателен.
	SecretsKey string `env:"AGY_SECRETS_KEY"`

	// Деньги и время.
	Currency string `env:"AGY_CURRENCY" envDefault:"USD"`
	Timezone string `env:"AGY_TIMEZONE" envDefault:"Europe/Moscow"`

	// Очередь и асинхронные задачи.
	NATSURL        string        `env:"NATS_URL" envDefault:"nats://nats:4222"`
	JobsTTL        time.Duration `env:"AGY_JOBS_TTL" envDefault:"86400"`
	JobMaxAttempts int           `env:"AGY_JOB_MAX_ATTEMPTS" envDefault:"5"`

	// Файлы-результаты и песочница навыков.
	FilesTTL       time.Duration `env:"AGY_FILES_TTL" envDefault:"86400"`
	SandboxCPU     float64       `env:"AGY_SANDBOX_CPU" envDefault:"2"`
	SandboxMemory  ByteSize      `env:"AGY_SANDBOX_MEMORY" envDefault:"2GiB"`
	SandboxTimeout time.Duration `env:"AGY_SANDBOX_TIMEOUT" envDefault:"300"`

	// Служебные.
	HTTPAddr    string `env:"AGY_HTTP_ADDR" envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://agy:agy@postgres:5432/agy?sslmode=disable"`
	DataDir     string `env:"AGY_DATA_DIR" envDefault:"/data"`
	LogLevel    string `env:"AGY_LOG_LEVEL" envDefault:"info"`
	SandboxdURL string `env:"AGY_SANDBOXD_URL" envDefault:"http://sandboxd:8090"`

	// Вычисляемые при загрузке.
	Location      *time.Location `env:"-"`
	SecretsKeyRaw []byte         `env:"-"`
}

// Load читает настройки из окружения процесса и проверяет их.
func Load() (Config, error) {
	return load(env.Options{})
}

// LoadFrom читает настройки из переданного набора переменных (для тестов).
func LoadFrom(environ map[string]string) (Config, error) {
	return load(env.Options{Environment: environ})
}

func load(opts env.Options) (Config, error) {
	opts.FuncMap = map[reflect.Type]env.ParserFunc{
		reflect.TypeOf(time.Duration(0)): parseSeconds,
	}

	var cfg Config
	if err := env.ParseWithOptions(&cfg, opts); err != nil {
		return Config{}, fmt.Errorf("read config: %w", withEnvNames(err))
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate проверяет значения и заполняет вычисляемые поля.
func (c *Config) Validate() error {
	var errs []error

	positiveDurations := []struct {
		name string
		v    time.Duration
	}{
		{"AGY_TIMEOUT_MAX", c.TimeoutMax},
		{"AGY_QUOTA_COOLDOWN", c.QuotaCooldown},
		{"AGY_AUTH_COOLDOWN", c.AuthCooldown},
		{"AGY_REGION_COOLDOWN", c.RegionCooldown},
		{"AGY_ENDPOINT_COOLDOWN", c.EndpointCooldown},
		{"AGY_JOBS_TTL", c.JobsTTL},
		{"AGY_FILES_TTL", c.FilesTTL},
		{"AGY_SANDBOX_TIMEOUT", c.SandboxTimeout},
	}
	for _, d := range positiveDurations {
		if d.v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", d.name))
		}
	}

	positiveInts := []struct {
		name string
		v    int64
	}{
		{"AGY_CONCURRENCY", int64(c.Concurrency)},
		{"AGY_LOG_DAYS", int64(c.LogDays)},
		{"AGY_ATTACH_MAX_FILES", int64(c.AttachMaxFiles)},
		{"AGY_ATTACH_MAX_BYTES", int64(c.AttachMaxBytes)},
		{"AGY_JOB_MAX_ATTEMPTS", int64(c.JobMaxAttempts)},
		{"AGY_SANDBOX_MEMORY", int64(c.SandboxMemory)},
	}
	for _, n := range positiveInts {
		if n.v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", n.name))
		}
	}

	if c.SandboxCPU <= 0 {
		errs = append(errs, errors.New("AGY_SANDBOX_CPU must be positive"))
	}

	if len(c.Currency) != 3 || strings.ToUpper(c.Currency) != c.Currency {
		errs = append(errs, fmt.Errorf("AGY_CURRENCY must be a 3-letter upper-case code, got %q", c.Currency))
	}

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil || c.Timezone == "" {
		errs = append(errs, fmt.Errorf("AGY_TIMEZONE: unknown time zone %q", c.Timezone))
	} else {
		c.Location = loc
	}

	c.SecretsKeyRaw = nil
	if c.SecretsKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.SecretsKey)
		switch {
		case err != nil:
			errs = append(errs, errors.New("AGY_SECRETS_KEY must be base64"))
		case len(key) != SecretsKeySize:
			errs = append(errs, fmt.Errorf("AGY_SECRETS_KEY must decode to %d bytes, got %d", SecretsKeySize, len(key)))
		default:
			c.SecretsKeyRaw = key
		}
	}

	for name, v := range map[string]string{
		"AGY_HTTP_ADDR":    c.HTTPAddr,
		"DATABASE_URL":     c.DatabaseURL,
		"NATS_URL":         c.NATSURL,
		"AGY_DATA_DIR":     c.DataDir,
		"AGY_SANDBOXD_URL": c.SandboxdURL,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s must not be empty", name))
		}
	}

	if _, err := logging.ParseLevel(c.LogLevel); err != nil {
		errs = append(errs, fmt.Errorf("AGY_LOG_LEVEL: %w", err))
	}

	return errors.Join(errs...)
}

// AccountsDir — домашние папки аккаунтов agy.
func (c Config) AccountsDir() string { return filepath.Join(c.DataDir, "accounts") }

// SkillsDir — каталог навыков.
func (c Config) SkillsDir() string { return filepath.Join(c.DataDir, "skills") }

// WorkDir — рабочие папки запросов.
func (c Config) WorkDir() string { return filepath.Join(c.DataDir, "work") }

// withEnvNames заменяет в ошибках разбора имена полей Go на имена переменных окружения:
// администратору нужно знать, какую переменную исправить.
func withEnvNames(err error) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return err
	}

	t := reflect.TypeFor[Config]()
	errs := make([]error, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		var pe env.ParseError
		if errors.As(e, &pe) {
			if f, ok := t.FieldByName(pe.Name); ok {
				if name := f.Tag.Get("env"); name != "" {
					errs = append(errs, fmt.Errorf("%s: %w", name, pe.Err))

					continue
				}
			}
		}
		errs = append(errs, e)
	}

	return errors.Join(errs...)
}

func parseSeconds(v string) (any, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("expected whole number of seconds, got %q", v)
	}

	return time.Duration(n) * time.Second, nil
}

// ByteSize — размер в байтах. В окружении: число байт или число с суффиксом
// KiB, MiB, GiB (KB, MB, GB, K, M, G — тоже степени 1024).
type ByteSize int64

// UnmarshalText разбирает размер из строки окружения.
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.ToUpper(strings.TrimSpace(string(text)))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
		{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1},
	} {
		if strings.HasSuffix(s, u.suffix) {
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			mult = u.mult

			break
		}
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid size %q", string(text))
	}
	if n > math.MaxInt64/mult || n < math.MinInt64/mult {
		return fmt.Errorf("size %q is too large", string(text))
	}
	*b = ByteSize(n * mult)

	return nil
}

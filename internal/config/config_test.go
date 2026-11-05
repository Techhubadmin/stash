package config

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This Config is parsed with env.Options{RequiredIfNoDef: true}, which means a
// struct tag WITHOUT an `envDefault` is silently mandatory. That is a trap: a
// field meant to be optional boots fine on any machine where it happens to be
// set, and hard-fails everywhere else.
//
// It bit us for real on 2026-08-14 — the shadow embedding fields shipped
// without defaults, which turned an explicitly opt-in feature into three newly
// required variables. Production caught it as a failed healthcheck
// ("required environment variable STASH_SHADOW_OPENAI_BASE_URL is not set")
// and rolled back, but the deploy was dead on arrival.
func TestShadowConfigFieldsAreOptional(t *testing.T) {
	optional := []string{
		"ShadowEmbeddingModel",
		"ShadowEmbeddingAPIKey",
		"ShadowEmbeddingBaseURL",
		"ShadowVectorDim",
		"ShadowMigrateBatch",
	}

	typ := reflect.TypeOf(Config{})
	for _, name := range optional {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Config has no field %s", name)
		}
		tag := string(field.Tag)
		if !strings.Contains(tag, "envDefault:") {
			t.Errorf("%s has no envDefault; with RequiredIfNoDef:true that makes it MANDATORY "+
				"and every deployment without it will fail to boot. Tag: %s", name, tag)
		}
		if strings.Contains(tag, ",required") {
			t.Errorf("%s is marked required but shadow embedding is opt-in. Tag: %s", name, tag)
		}
	}
}

// Set every field explicitly so tests do not inherit the host configuration.
func validConfigFromEnvironment(t *testing.T) *Config {
	t.Helper()
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("env"), ",")[0]
		value, ok := field.Tag.Lookup("envDefault")
		if !ok {
			switch field.Name {
			case "VectorDim":
				value = "1024"
			case "MaxResultSize":
				value = "10"
			case "ContextTTL":
				value = "1h"
			default:
				value = "test"
			}
		}
		t.Setenv(name, value)
	}
	cfg, err := NewFromFile(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("valid environment: %v", err)
	}
	return cfg
}

func TestValidateValidConfig(t *testing.T) {
	cfg := validConfigFromEnvironment(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateInvalidValues(t *testing.T) {
	base := validConfigFromEnvironment(t)
	cases := []struct {
		field string
		value any
		rule  string
	}{
		{"ConsolidationWindow", "soon", "must parse as a duration and be > 0"},
		{"ConsolidationWindow", "0s", "must parse as a duration and be > 0"},
		{"ConsolidationWindow", "-1s", "must parse as a duration and be > 0"},
		{"VectorDim", 0, "must be >= 1"},
		{"VectorDim", 2001, "must be <= 2000"},
		{"ShadowVectorDim", 2001, "must be <= 2000"},
		{"MaxResultSize", 0, "must be >= 1"},
		{"ConsolidationBatchSize", 0, "must be >= 1"},
		{"RetrievalOverfetchFactor", 0, "must be >= 1"},
		{"EmbeddingBackfillBatch", 0, "must be >= 1"},
		{"MaxRecordAttempts", 0, "must be >= 1"},
		{"CycleReasonerCallCeiling", 0, "must be >= 1"},
		{"ShadowMigrateBatch", 0, "must be >= 1"},
		{"ProviderRerankCandidateLimit", 0, "must be >= 1"},
		{"ReasonerMaxTokens", int64(0), "must be >= 1"},
		{"EmbedderMaxRetries", -1, "must be >= 0"},
		{"ReasonerMaxRetries", -1, "must be >= 0"},
	}
	for _, field := range []string{
		"ConsolidationSimilarityThreshold", "ConsolidationDedupThreshold", "DecayFactor",
		"ExpiryThreshold", "HypothesisAutoConfirmThreshold", "HypothesisAutoRejectThreshold",
		"RetrievalUtilityWeight", "RetrievalMaxUtilityDelta",
	} {
		for _, value := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
			cases = append(cases, struct {
				field string
				value any
				rule  string
			}{field, value, "must be within [0, 1]"})
		}
	}
	for _, field := range []string{
		"EmbedderRateLimitCooldown", "EmbedderPaymentCooldown", "EmbedderServerErrorCooldown",
		"ReasonerRateLimitCooldown", "ReasonerPaymentCooldown", "ReasonerServerErrorCooldown",
		"ContextTTL", "RecallHistoryRetention",
	} {
		cases = append(cases, struct {
			field string
			value any
			rule  string
		}{field, -time.Second, "must be >= 0"})
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			cfg := *base
			field := reflect.ValueOf(&cfg).Elem().FieldByName(tc.field)
			field.Set(reflect.ValueOf(tc.value).Convert(field.Type()))
			tag, _ := reflect.TypeOf(cfg).FieldByName(tc.field)
			name := strings.Split(tag.Tag.Get("env"), ",")[0]
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), name+"=") || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("Validate() = %v, want %s and %q", err, name, tc.rule)
			}
			if strings.Count(err.Error(), "\n") != 0 {
				t.Fatalf("expected exactly one violation, got %v", err)
			}
		})
	}
}

func TestNewFromFileReturnsAllValidationErrors(t *testing.T) {
	validConfigFromEnvironment(t)
	t.Setenv("STASH_CONSOLIDATION_WINDOW", "soon")
	t.Setenv("STASH_DECAY_FACTOR", "2")
	cfg, err := NewFromFile(filepath.Join(t.TempDir(), "missing.env"))
	if cfg != nil || err == nil {
		t.Fatalf("NewFromFile() = %v, %v; want nil config and validation error", cfg, err)
	}
	for _, want := range []string{"STASH_CONSOLIDATION_WINDOW=soon", "STASH_DECAY_FACTOR=2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Count(err.Error(), "\n") != 1 {
		t.Fatalf("expected two violation lines, got %v", err)
	}
}

func TestShadowEnabledNeedsBothModelAndCredential(t *testing.T) {
	// A model without a key would 403 on every call; a key without a model has
	// nothing to embed with. Either alone must read as "not configured" rather
	// than half-arming the migration.
	cases := []struct {
		name  string
		model string
		key   string
		want  bool
	}{
		{"neither", "", "", false},
		{"model only", "Qwen/Qwen3-Embedding-8B", "", false},
		{"credential only", "", "sk-test", false},
		{"both", "Qwen/Qwen3-Embedding-8B", "sk-test", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{ShadowEmbeddingModel: c.model, ShadowEmbeddingAPIKey: c.key}
			if got := cfg.ShadowEnabled(); got != c.want {
				t.Fatalf("ShadowEnabled() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProviderRerankConfigIsOptionalAndNeedsModelAndCredential(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	for _, name := range []string{"ProviderRerankModel", "ProviderRerankAPIKey", "ProviderRerankBaseURL", "ProviderRerankCandidateLimit"} {
		field, ok := typ.FieldByName(name)
		if !ok || !strings.Contains(string(field.Tag), "envDefault:") {
			t.Fatalf("%s must remain optional, field=%+v", name, field)
		}
	}
	if (&Config{ProviderRerankModel: "Qwen/Qwen3-Reranker-0.6B"}).ProviderRerankEnabled() {
		t.Fatal("model without credential must not enable provider reranking")
	}
	if (&Config{ProviderRerankAPIKey: "test"}).ProviderRerankEnabled() {
		t.Fatal("credential without model must not enable provider reranking")
	}
	if !(&Config{ProviderRerankModel: "Qwen/Qwen3-Reranker-0.6B", ProviderRerankAPIKey: "test"}).ProviderRerankEnabled() {
		t.Fatal("model and credential must enable provider reranking")
	}
}

func TestShadowBaseURLFallsBackToTheLiveEndpoint(t *testing.T) {
	cfg := &Config{OpenAIBaseURL: "https://api.deepinfra.com/v1/openai"}
	if got := cfg.ShadowBaseURL(); got != "https://api.deepinfra.com/v1/openai" {
		t.Fatalf("unset shadow base URL should fall back to the live one, got %q", got)
	}

	cfg.ShadowEmbeddingBaseURL = "https://other.example/v1"
	if got := cfg.ShadowBaseURL(); got != "https://other.example/v1" {
		t.Fatalf("explicit shadow base URL must win, got %q", got)
	}
}

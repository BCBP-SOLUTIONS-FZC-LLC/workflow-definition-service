package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestLoad_Success(t *testing.T) {
	envBackup := make(map[string]string)
	keysToBackup := []string{
		"DATABASE_URL", "APP_ENV", "BUILD_VERSION", "HTTP_PORT", "GRPC_PORT",
		"OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_INSECURE",
		"OTEL_TRACES_SAMPLER_RATIO", "PG_MAX_CONNS", "PG_MIN_CONNS",
		"PG_SLOW_QUERY_THRESHOLD_MS", "VALKEY_ADDR", "VALKEY_PASSWORD",
		"AWS_USE_STUB", "AWS_REGION", "SNS_TOPIC_ARN",
		"OUTBOX_POLL_INTERVAL", "OUTBOX_BATCH_SIZE", "ORG_MEMBERSHIP_BASE_URL",
		"EXECUTION_SERVICE_ADDR", "GLUE_REGISTRY_NAME", "GLUE_REGISTRY_ARN",
	}
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	_ = os.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("BUILD_VERSION", "v1.2.3")
	_ = os.Setenv("HTTP_PORT", "9000")
	_ = os.Setenv("GRPC_PORT", "9001")
	_ = os.Setenv("OTEL_SERVICE_NAME", "custom-service")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel:4317")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	_ = os.Setenv("OTEL_TRACES_SAMPLER_RATIO", "0.5")
	_ = os.Setenv("PG_MAX_CONNS", "20")
	_ = os.Setenv("PG_MIN_CONNS", "5")
	_ = os.Setenv("PG_SLOW_QUERY_THRESHOLD_MS", "500")
	_ = os.Setenv("VALKEY_ADDR", "valkey:6379")
	_ = os.Setenv("VALKEY_PASSWORD", "secret")
	_ = os.Setenv("AWS_USE_STUB", "false")
	_ = os.Setenv("AWS_REGION", "us-west-2")
	_ = os.Setenv("SNS_TOPIC_ARN", "arn:aws:sns:topic")
	_ = os.Setenv("OUTBOX_POLL_INTERVAL", "1s")
	_ = os.Setenv("OUTBOX_BATCH_SIZE", "100")
	_ = os.Setenv("ORG_MEMBERSHIP_BASE_URL", "http://org")
	_ = os.Setenv("EXECUTION_SERVICE_ADDR", "execution:9090")
	_ = os.Setenv("GLUE_REGISTRY_NAME", "workflow-template-events")
	_ = os.Setenv("GLUE_REGISTRY_ARN", "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost:5432/test" {
		t.Errorf("DatabaseURL = %q, want postgres://localhost:5432/test", cfg.DatabaseURL)
	}
	if cfg.AppEnv != "production" {
		t.Errorf("AppEnv = %q, want production", cfg.AppEnv)
	}
	if cfg.BuildVersion != "v1.2.3" {
		t.Errorf("BuildVersion = %q, want v1.2.3", cfg.BuildVersion)
	}
	if cfg.HTTPPort != 9000 {
		t.Errorf("HTTPPort = %d, want 9000", cfg.HTTPPort)
	}
	if cfg.GRPCPort != 9001 {
		t.Errorf("GRPCPort = %d, want 9001", cfg.GRPCPort)
	}
	if cfg.OTELServiceName != "custom-service" {
		t.Errorf("OTELServiceName = %q, want custom-service", cfg.OTELServiceName)
	}
	if cfg.OTELExporterEndpoint != "otel:4317" {
		t.Errorf("OTELExporterEndpoint = %q, want otel:4317", cfg.OTELExporterEndpoint)
	}
	if cfg.OTELExporterInsecure {
		t.Errorf("OTELExporterInsecure = %t, want false", cfg.OTELExporterInsecure)
	}
	if cfg.OTELTracesSamplerRatio != 0.5 {
		t.Errorf("OTELTracesSamplerRatio = %f, want 0.5", cfg.OTELTracesSamplerRatio)
	}
	if cfg.PGMaxConns != 20 {
		t.Errorf("PGMaxConns = %d, want 20", cfg.PGMaxConns)
	}
	if cfg.PGMinConns != 5 {
		t.Errorf("PGMinConns = %d, want 5", cfg.PGMinConns)
	}
	if cfg.PGSlowQueryThresholdMS != 500 {
		t.Errorf("PGSlowQueryThresholdMS = %d, want 500", cfg.PGSlowQueryThresholdMS)
	}
	if cfg.ValkeyAddr != "valkey:6379" {
		t.Errorf("ValkeyAddr = %q, want valkey:6379", cfg.ValkeyAddr)
	}
	if cfg.ValkeyPassword != "secret" {
		t.Errorf("ValkeyPassword = %q, want secret", cfg.ValkeyPassword)
	}
	if cfg.AWSUseStub {
		t.Errorf("AWSUseStub = %t, want false", cfg.AWSUseStub)
	}
	if cfg.AWSRegion != "us-west-2" {
		t.Errorf("AWSRegion = %q, want us-west-2", cfg.AWSRegion)
	}
	if cfg.SNSTopicARN != "arn:aws:sns:topic" {
		t.Errorf("SNSTopicARN = %q, want arn:aws:sns:topic", cfg.SNSTopicARN)
	}
	if cfg.OutboxPollInterval != time.Second {
		t.Errorf("OutboxPollInterval = %v, want 1s", cfg.OutboxPollInterval)
	}
	if cfg.OutboxBatchSize != 100 {
		t.Errorf("OutboxBatchSize = %d, want 100", cfg.OutboxBatchSize)
	}
	if cfg.OrgMembershipBaseURL != "http://org" {
		t.Errorf("OrgMembershipBaseURL = %q, want http://org", cfg.OrgMembershipBaseURL)
	}
	if cfg.ExecutionServiceAddr != "execution:9090" {
		t.Errorf("ExecutionServiceAddr = %q, want execution:9090", cfg.ExecutionServiceAddr)
	}
	if cfg.GlueRegistryName != "workflow-template-events" {
		t.Errorf("GlueRegistryName = %q, want workflow-template-events", cfg.GlueRegistryName)
	}
	if cfg.GlueRegistryARN != "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events" {
		t.Errorf("GlueRegistryARN = %q, want arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events", cfg.GlueRegistryARN)
	}
}

func TestLoad_ValidationFailures(t *testing.T) {
	keysToBackup := []string{
		"DATABASE_URL", "AWS_USE_STUB", "SNS_TOPIC_ARN", "APP_ENV",
		"ORG_MEMBERSHIP_BASE_URL", "EXECUTION_SERVICE_ADDR",
		"GLUE_REGISTRY_NAME", "GLUE_REGISTRY_ARN",
	}
	envBackup := make(map[string]string)
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: true,
		},
		{
			name: "missing sns topic when aws stub is false",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN":      "",
				"GLUE_REGISTRY_NAME": "workflow-template-events",
				"GLUE_REGISTRY_ARN":  "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events",
			},
			wantErr: true,
		},
		{
			name: "valid when aws stub is true even without sns",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "true",
				"SNS_TOPIC_ARN": "",
			},
			wantErr: false,
		},
		{
			name: "missing org membership url in non-dev when stub is false",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN": "arn:aws:sns:topic", "APP_ENV": "production",
				"ORG_MEMBERSHIP_BASE_URL": "", "EXECUTION_SERVICE_ADDR": "execution:9090",
				"GLUE_REGISTRY_NAME": "workflow-template-events",
				"GLUE_REGISTRY_ARN":  "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events",
			},
			wantErr: true,
		},
		{
			name: "missing execution addr in non-dev when stub is false",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN": "arn:aws:sns:topic", "APP_ENV": "production",
				"ORG_MEMBERSHIP_BASE_URL": "http://org", "EXECUTION_SERVICE_ADDR": "",
				"GLUE_REGISTRY_NAME": "workflow-template-events",
				"GLUE_REGISTRY_ARN":  "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events",
			},
			wantErr: true,
		},
		{
			name: "valid in dev when stub is false without org/execution",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN": "arn:aws:sns:topic", "APP_ENV": "dev",
				"ORG_MEMBERSHIP_BASE_URL": "", "EXECUTION_SERVICE_ADDR": "",
				"GLUE_REGISTRY_NAME": "workflow-template-events",
				"GLUE_REGISTRY_ARN":  "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events",
			},
			wantErr: false,
		},
		{
			name: "missing glue registry name when aws stub is false",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN":      "arn:aws:sns:topic",
				"GLUE_REGISTRY_NAME": "",
				"GLUE_REGISTRY_ARN":  "arn:aws:glue:us-east-1:123456789012:registry/workflow-template-events",
			},
			wantErr: true,
		},
		{
			name: "missing glue registry arn when aws stub is false",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost", "AWS_USE_STUB": "false",
				"SNS_TOPIC_ARN":      "arn:aws:sns:topic",
				"GLUE_REGISTRY_NAME": "workflow-template-events",
				"GLUE_REGISTRY_ARN":  "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range keysToBackup {
				_ = os.Unsetenv(k)
			}
			for k, v := range tt.env {
				_ = os.Setenv(k, v)
			}
			_, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoad_InvalidValues_FallBackToDefaults(t *testing.T) {
	keysToBackup := []string{"HTTP_PORT", "OTEL_EXPORTER_OTLP_INSECURE", "OTEL_TRACES_SAMPLER_RATIO", "OUTBOX_POLL_INTERVAL"}
	envBackup := make(map[string]string)
	for _, k := range keysToBackup {
		envBackup[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range envBackup {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	_ = os.Setenv("DATABASE_URL", "postgres://localhost")
	_ = os.Setenv("HTTP_PORT", "not-an-int")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "not-a-bool")
	_ = os.Setenv("OTEL_TRACES_SAMPLER_RATIO", "not-a-float")
	_ = os.Setenv("OUTBOX_POLL_INTERVAL", "not-a-duration")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort fallback = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.OTELExporterInsecure {
		t.Errorf("OTELExporterInsecure fallback = %t, want false", cfg.OTELExporterInsecure)
	}
	if cfg.OTELTracesSamplerRatio != 1.0 {
		t.Errorf("OTELTracesSamplerRatio fallback = %f, want 1.0", cfg.OTELTracesSamplerRatio)
	}
	if cfg.OutboxPollInterval != 500*time.Millisecond {
		t.Errorf("OutboxPollInterval fallback = %v, want 500ms", cfg.OutboxPollInterval)
	}
}

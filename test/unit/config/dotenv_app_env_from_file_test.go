package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

// Not parallel: the cases change the working directory and the environment.
func TestLoad_AppEnvOnlyInDotEnv_IsHonouredForDevAndIgnoredOtherwise(t *testing.T) {
	cases := []struct {
		name          string
		dotEnv        string
		wantErr       string
		wantAppEnv    string
		wantHTTPPort  int
		wantPortInEnv string
	}{
		{
			name:          "dev in the file loads every value from it",
			dotEnv:        "APP_ENV=dev\nDATABASE_URL=postgres://localhost/db\nHTTP_PORT=1234\n",
			wantAppEnv:    "dev",
			wantHTTPPort:  1234,
			wantPortInEnv: "1234",
		},
		{
			name:    "prod in the file loads nothing and APP_ENV stays unset",
			dotEnv:  "APP_ENV=prod\nDATABASE_URL=postgres://localhost/db\nHTTP_PORT=1234\n",
			wantErr: "APP_ENV is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.dotEnv), 0o600))
			t.Chdir(dir)
			isolateEnv(t, nil)

			cfg, err := config.Load()

			assert.Equal(t, tc.wantPortInEnv, os.Getenv("HTTP_PORT"))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.True(t, strings.Contains(err.Error(), tc.wantErr), "error = %v", err)
				assert.Empty(t, os.Getenv("APP_ENV"), "the file's APP_ENV must not be exported")
				assert.Empty(t, os.Getenv("DATABASE_URL"), "no value from the file may be exported")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantAppEnv, cfg.AppEnv)
			assert.Equal(t, tc.wantHTTPPort, cfg.HTTPPort)
			assert.Equal(t, "postgres://localhost/db", cfg.DatabaseURL)
		})
	}
}

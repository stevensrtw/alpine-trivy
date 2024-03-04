//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	awscommands "github.com/aquasecurity/trivy/pkg/cloud/aws/commands"
	"github.com/aquasecurity/trivy/pkg/flag"
)

func TestAwsCommandRun(t *testing.T) {
	tests := []struct {
		name    string
		options flag.Options
		envs    map[string]string
		wantErr string
	}{
		{
			name: "fail without region",
			options: flag.Options{
				RegoOptions: flag.RegoOptions{SkipPolicyUpdate: true},
			},
			envs: map[string]string{
				"AWS_ACCESS_KEY_ID":     "test",
				"AWS_SECRET_ACCESS_KEY": "test",
			},
			wantErr: "aws region is required",
		},
		{
			name: "fail without creds",
			envs: map[string]string{
				"AWS_PROFILE": "non-existent-profile",
			},
			options: flag.Options{
				RegoOptions: flag.RegoOptions{SkipPolicyUpdate: true},
				AWSOptions: flag.AWSOptions{
					Region: "us-east-1",
				},
			},
			wantErr: "non-existent-profile",
		},
	}

	ctx := context.Background()

	localstackC, addr := setupLocalStack(t, ctx)
	defer localstackC.Terminate(ctx)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			tt.options.AWSOptions.Endpoint = addr
			tt.options.GlobalOptions.Timeout = time.Minute

			for k, v := range tt.envs {
				t.Setenv(k, v)
			}

			err := awscommands.Run(context.Background(), tt.options)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr, tt.name)
				return
			}
			assert.NoError(t, err)
		})
	}

}

func setupLocalStack(t *testing.T, ctx context.Context) (*localstack.LocalStackContainer, string) {
	t.Helper()
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	container, err := localstack.RunContainer(ctx, testcontainers.CustomizeRequest(
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "localstack/localstack:2.2.0",
				HostConfigModifier: func(hostConfig *dockercontainer.HostConfig) {
					hostConfig.AutoRemove = true
				},
			},
		},
	))
	require.NoError(t, err)

	p, err := container.MappedPort(ctx, "4566/tcp")
	require.NoError(t, err)

	provider, err := testcontainers.NewDockerProvider()
	require.NoError(t, err)
	defer provider.Close()

	host, err := provider.DaemonHost(ctx)
	require.NoError(t, err)

	return container, fmt.Sprintf("http://%s:%d", host, p.Int())

}

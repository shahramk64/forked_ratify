/*
Copyright The Ratify Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
	ratifyerrors "github.com/ratify-project/ratify/errors"
	"github.com/ratify-project/ratify/pkg/common/oras/authprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockAuthClient struct {
	mock.Mock
}

type MockAzureAuth struct {
	mock.Mock
}

type MockAuthClientFactory struct {
	mock.Mock
}

func (m *MockAuthClientFactory) NewAuthenticationClient(serverURL string, options *azcontainerregistry.AuthenticationClientOptions) (AuthClient, error) {
	args := m.Called(serverURL, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(AuthClient), args.Error(1)
}

func (m *MockAzureAuth) GetAADAccessToken(ctx context.Context, tenantID, clientID, resource string) (confidential.AuthResult, error) {
	args := m.Called(ctx, tenantID, clientID, resource)
	return args.Get(0).(confidential.AuthResult), args.Error(1)
}

func (m *MockAuthClient) ExchangeAADAccessTokenForACRRefreshToken(ctx context.Context, grantType, service string, options *azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenOptions) (azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenResponse, error) {
	args := m.Called(ctx, grantType, service, options)
	return args.Get(0).(azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenResponse), args.Error(1)
}

// Verifies that Enabled checks if tenantID is empty or AAD token is empty
func TestAzureWIEnabled_ExpectedResults(t *testing.T) {
	azAuthProvider := WIAuthProvider{
		tenantID: "test_tenant",
		clientID: "test_client",
		aadToken: confidential.AuthResult{
			AccessToken: "test_token",
		},
	}

	ctx := context.Background()

	if !azAuthProvider.Enabled(ctx) {
		t.Fatal("enabled should have returned true but returned false")
	}

	azAuthProvider.tenantID = ""
	if azAuthProvider.Enabled(ctx) {
		t.Fatal("enabled should have returned false but returned true for empty tenantID")
	}

	azAuthProvider.clientID = ""
	if azAuthProvider.Enabled(ctx) {
		t.Fatal("enabled should have returned false but returned true for empty clientID")
	}

	azAuthProvider.aadToken.AccessToken = ""
	if azAuthProvider.Enabled(ctx) {
		t.Fatal("enabled should have returned false but returned true for empty AAD access token")
	}
}

func TestGetEarliestExpiration(t *testing.T) {
	var aadExpiry = time.Now().Add(12 * time.Hour)

	if getACRExpiryIfEarlier(aadExpiry) == aadExpiry {
		t.Fatal("expected acr token expiry time")
	}

	aadExpiry = time.Now().Add(12 * time.Minute)

	if getACRExpiryIfEarlier(aadExpiry) != aadExpiry {
		t.Fatal("expected aad token expiry time")
	}
}

// Verifies that tenant id, client id, token file path, and authority host
// environment variables are properly set
func TestAzureWIValidation_EnvironmentVariables_ExpectedResults(t *testing.T) {
	authProviderConfig := map[string]interface{}{
		"name": "azureWorkloadIdentity",
	}

	err := os.Setenv("AZURE_TENANT_ID", "")
	if err != nil {
		t.Fatal("failed to set env variable AZURE_TENANT_ID")
	}

	_, err = authprovider.CreateAuthProviderFromConfig(authProviderConfig)

	expectedErr := ratifyerrors.ErrorCodeAuthDenied.WithDetail("azure tenant id environment variable is empty")
	if err == nil || !errors.Is(err, expectedErr) {
		t.Fatalf("create auth provider should have failed: expected err %s, but got err %s", expectedErr, err)
	}

	err = os.Setenv("AZURE_TENANT_ID", "tenant id")
	if err != nil {
		t.Fatal("failed to set env variable AZURE_TENANT_ID")
	}

	authProviderConfigWithClientID := map[string]interface{}{
		"name":     "azureWorkloadIdentity",
		"clientID": "client id from config",
	}

	_, err = authprovider.CreateAuthProviderFromConfig(authProviderConfigWithClientID)

	expectedErr = ratifyerrors.ErrorCodeAuthDenied.WithDetail("required environment variables not set, AZURE_FEDERATED_TOKEN_FILE: , AZURE_AUTHORITY_HOST: ")
	if err == nil || !errors.Is(err, expectedErr) {
		t.Fatalf("create auth provider should have failed: expected err %s, but got err %s", expectedErr, err)
	}

	_, err = authprovider.CreateAuthProviderFromConfig(authProviderConfig)

	expectedErr = ratifyerrors.ErrorCodeAuthDenied.WithDetail("no client ID provided and AZURE_CLIENT_ID environment variable is empty")
	if err == nil || !errors.Is(err, expectedErr) {
		t.Fatalf("create auth provider should have failed: expected err %s, but got err %s", expectedErr, err)
	}

	err = os.Setenv("AZURE_CLIENT_ID", "client id")
	if err != nil {
		t.Fatal("failed to set env variable AZURE_CLIENT_ID")
	}

	defer os.Unsetenv("AZURE_CLIENT_ID")
	defer os.Unsetenv("AZURE_TENANT_ID")

	_, err = authprovider.CreateAuthProviderFromConfig(authProviderConfig)

	expectedErr = ratifyerrors.ErrorCodeAuthDenied.WithDetail("required environment variables not set, AZURE_FEDERATED_TOKEN_FILE: , AZURE_AUTHORITY_HOST: ")
	if err == nil || !errors.Is(err, expectedErr) {
		t.Fatalf("create auth provider should have failed: expected err %s, but got err %s", expectedErr, err)
	}
}

func TestNewAzureWIAuthProvider_AuthenticationClientError(t *testing.T) {
	// Create a new mock client factory
	mockFactory := new(MockAuthClientFactory)

	// Setup mock to return an error
	mockFactory.On("NewAuthenticationClient", mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to create authentication client"))

	// Create a new WIAuthProvider instance
	provider := NewAzureWIAuthProvider()
	provider.authClientFactory = mockFactory.NewAuthenticationClient

	// Call authClientFactory to test error handling
	_, err := provider.authClientFactory("https://myregistry.azurecr.io", nil)

	// Assert that an error is returned
	assert.Error(t, err)
	assert.Equal(t, "failed to create authentication client", err.Error())

	// Verify that the mock was called
	mockFactory.AssertCalled(t, "NewAuthenticationClient", "https://myregistry.azurecr.io", mock.Anything)
}

func TestNewAzureWIAuthProvider_Success(t *testing.T) {
	// Create a new mock client factory
	mockFactory := new(MockAuthClientFactory)

	// Create a mock auth client to return from the factory
	mockAuthClient := new(MockAuthClient)

	// Setup mock to return a successful auth client
	mockFactory.On("NewAuthenticationClient", mock.Anything, mock.Anything).
		Return(mockAuthClient, nil)

	// Create a new WIAuthProvider instance
	provider := NewAzureWIAuthProvider()

	// Replace authClientFactory with the mock factory
	provider.authClientFactory = mockFactory.NewAuthenticationClient

	// Call authClientFactory to test successful return
	client, err := provider.authClientFactory("https://myregistry.azurecr.io", nil)

	// Assert that the client is returned without an error
	assert.NoError(t, err)
	assert.NotNil(t, client)

	// Assert that the returned client is of the expected type
	_, ok := client.(*MockAuthClient)
	assert.True(t, ok, "expected client to be of type *MockAuthClient")

	// Verify that the mock was called
	mockFactory.AssertCalled(t, "NewAuthenticationClient", "https://myregistry.azurecr.io", mock.Anything)
}

func TestWIProvide_Success(t *testing.T) {
	mockClient := new(MockAuthClient)
	expectedRefreshToken := "mocked_refresh_token"
	mockClient.On("ExchangeAADAccessTokenForACRRefreshToken", mock.Anything, "access_token", "myregistry.azurecr.io", mock.Anything).
		Return(azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenResponse{
			ACRRefreshToken: azcontainerregistry.ACRRefreshToken{RefreshToken: &expectedRefreshToken},
		}, nil)

	provider := &WIAuthProvider{
		aadToken: confidential.AuthResult{
			AccessToken: "mockToken",
			ExpiresOn:   time.Now().Add(time.Hour),
		},
		tenantID: "mockTenantID",
		clientID: "mockClientID",
		authClientFactory: func(_ string, _ *azcontainerregistry.AuthenticationClientOptions) (AuthClient, error) {
			return mockClient, nil
		},
		getRegistryHost: func(_ string) (string, error) {
			return "myregistry.azurecr.io", nil
		},
		getAADAccessToken: func(_ context.Context, _, _, _ string) (confidential.AuthResult, error) {
			return confidential.AuthResult{
				AccessToken: "mockToken",
				ExpiresOn:   time.Now().Add(time.Hour),
			}, nil
		},
		reportMetrics: func(_ context.Context, _ int64, _ string) {},
	}

	authConfig, err := provider.Provide(context.Background(), "artifact")

	assert.NoError(t, err)
	// Assert that GetAADAccessToken was not called
	mockClient.AssertNotCalled(t, "GetAADAccessToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	// Assert that the returned refresh token matches the expected one
	assert.Equal(t, expectedRefreshToken, authConfig.Password)
}

func TestWIProvide_RefreshAAD(t *testing.T) {
	// Arrange
	mockAzureAuth := new(MockAzureAuth)
	mockClient := new(MockAuthClient)

	provider := &WIAuthProvider{
		aadToken: confidential.AuthResult{
			AccessToken: "mockToken",
			ExpiresOn:   time.Now(),
		},
		tenantID: "mockTenantID",
		clientID: "mockClientID",
		authClientFactory: func(_ string, _ *azcontainerregistry.AuthenticationClientOptions) (AuthClient, error) {
			return mockClient, nil
		},
		getRegistryHost: func(_ string) (string, error) {
			return "myregistry.azurecr.io", nil
		},
		getAADAccessToken: mockAzureAuth.GetAADAccessToken,
		reportMetrics:     func(_ context.Context, _ int64, _ string) {},
	}

	mockAzureAuth.On("GetAADAccessToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(confidential.AuthResult{AccessToken: "newAccessToken", ExpiresOn: time.Now().Add(time.Hour)}, nil)

	mockClient.On("ExchangeAADAccessTokenForACRRefreshToken", mock.Anything, "access_token", "myregistry.azurecr.io", mock.Anything).
		Return(azcontainerregistry.AuthenticationClientExchangeAADAccessTokenForACRRefreshTokenResponse{
			ACRRefreshToken: azcontainerregistry.ACRRefreshToken{RefreshToken: new(string)},
		}, nil)

	ctx := context.TODO()
	artifact := "testArtifact"

	// Act
	_, err := provider.Provide(ctx, artifact)

	assert.NoError(t, err)
	// Assert that GetAADAccessToken was not called
	mockAzureAuth.AssertCalled(t, "GetAADAccessToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestProvide_Failure_InvalidHostName(t *testing.T) {
	provider := &WIAuthProvider{
		getRegistryHost: func(_ string) (string, error) {
			return "", errors.New("invalid hostname")
		},
	}

	_, err := provider.Provide(context.Background(), "artifact")
	assert.Error(t, err)
}

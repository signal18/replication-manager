// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	v3TestAPIPassword = "apipw-v3-2261"
	v3TestDBPassword  = "dbpw-v3-2261"
)

// captureV3Log sends the global logrus output (the logger repmanv3.go writes to) to a buffer
func captureV3Log(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	out, level := log.StandardLogger().Out, log.GetLevel()
	log.SetOutput(&buf)
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		log.SetOutput(out)
		log.SetLevel(level)
	})
	return &buf
}

// newV3TestServer is a server holding one cluster whose configuration carries the API and
// DB credentials, and whose user admin holds the grants the v3 getters check
func newV3TestServer(t *testing.T) (*ReplicationManager, *cluster.Cluster) {
	t.Helper()
	cl := newTestClusterForAPI(t)
	cl.Conf.APIUsers = "admin:" + v3TestAPIPassword
	cl.Conf.User = "root:" + v3TestDBPassword
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{
		"api-credentials":       {Value: cl.Conf.APIUsers},
		"db-servers-credential": {Value: cl.Conf.User},
	})
	cl.APIUsers = map[string]cluster.APIUser{"admin": {
		User:     "admin",
		Password: v3TestAPIPassword,
		Grants:   map[string]bool{config.GrantClusterGrant: true, config.GrantClusterSettings: true},
	}}
	repman := &ReplicationManager{
		Conf:     &config.Config{APIUsers: cl.Conf.APIUsers},
		Clusters: map[string]*cluster.Cluster{cl.Name: cl},
	}
	return repman, cl
}

// v3TestContext carries a bearer JWT whose password claim getClusterAndUser accepts
func v3TestContext(t *testing.T) (context.Context, string) {
	t.Helper()
	(&ReplicationManager{}).initKeys()
	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["CustomUserInfo"] = map[string]interface{}{"Name": "admin", "Password": v3TestAPIPassword}
	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.SignedString(sk)
	if err != nil {
		t.Fatal(err)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token)), token
}

func assertNoSecret(t *testing.T, what, s string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(s, secret) {
			t.Errorf("%s carries %q", what, secret)
		}
	}
}

// #2261: the interceptors log the method, never the request (a setting value) or the server
// object (its configuration and secrets), and they ran for callers that sent no token
func TestV3InterceptorsLogMethodOnly(t *testing.T) {
	repman, cl := newV3TestServer(t)
	buf := captureV3Log(t)

	req := &v3.ClusterSetting{Cluster: &v3.Cluster{Name: cl.Name}, Setting: &v3.ClusterSetting_Setting{Value: "setting-value-2261"}}
	unary := &grpc.UnaryServerInfo{FullMethod: "/signal18.replication_manager.v3.ClusterService/SetActionForClusterSettings"}
	if _, err := repman.unaryInterceptor(context.Background(), req, unary, func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	stream := &grpc.StreamServerInfo{FullMethod: "/signal18.replication_manager.v3.ClusterService/RetrieveFromTopology"}
	if err := repman.streamInterceptor(repman, nil, stream, func(srv interface{}, stream grpc.ServerStream) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	logged := buf.String()
	assertNoSecret(t, "the interceptor log", logged, "setting-value-2261", v3TestAPIPassword)
	// %v of the server prints its whole struct: in the lab one such line held every cluster's credentials
	if strings.Contains(logged, "&{") {
		t.Errorf("an interceptor dumps a struct: %.300s", logged)
	}
	if !strings.Contains(logged, unary.FullMethod) || !strings.Contains(logged, stream.FullMethod) {
		t.Fatalf("the interceptors no longer log the method: %s", logged)
	}
}

// #2261: GetCluster and GetSettingsForCluster send the configuration masked, as REST does, and
// the bearer token is never logged
func TestV3GettersMaskSecretsAndLogNoToken(t *testing.T) {
	repman, cl := newV3TestServer(t)
	ctx, token := v3TestContext(t)
	buf := captureV3Log(t)

	got, err := repman.GetCluster(ctx, &v3.Cluster{Name: cl.Name})
	if err != nil {
		t.Fatal(err)
	}
	clusterJSON, _ := protojson.Marshal(got)
	assertNoSecret(t, "GetCluster", string(clusterJSON), v3TestAPIPassword, v3TestDBPassword)
	if v := got.Fields["config"].GetStructValue().GetFields()["apiCredentials"].GetStringValue(); v != config.SecretMask {
		t.Errorf("GetCluster config.apiCredentials = %q, want %q", v, config.SecretMask)
	}

	got, err = repman.GetSettingsForCluster(ctx, &v3.Cluster{Name: cl.Name})
	if err != nil {
		t.Fatal(err)
	}
	settingsJSON, _ := protojson.Marshal(got)
	assertNoSecret(t, "GetSettingsForCluster", string(settingsJSON), v3TestAPIPassword, v3TestDBPassword)
	if v := got.Fields["apiCredentials"].GetStringValue(); v != config.SecretMask {
		t.Errorf("GetSettingsForCluster apiCredentials = %q, want %q", v, config.SecretMask)
	}

	assertNoSecret(t, "the log", buf.String(), token)
}

// #2261: a setting submitted through v3 is not logged with its value (setClusterSetting logs
// the change itself, masked)
func TestV3SetSettingLogsNoValue(t *testing.T) {
	repman, cl := newV3TestServer(t)
	ctx, token := v3TestContext(t)
	buf := captureV3Log(t)

	// name UNSPECIFIED: refused after the grant check, so nothing is set
	_, err := repman.SetActionForClusterSettings(ctx, &v3.ClusterSetting{
		Cluster: &v3.Cluster{Name: cl.Name},
		Action:  v3.ClusterSetting_SET,
		Setting: &v3.ClusterSetting_Setting{Value: "setting-value-2261"},
	})
	if err == nil {
		t.Fatal("a setting with no name was accepted")
	}
	assertNoSecret(t, "the log", buf.String(), "setting-value-2261", token)
}

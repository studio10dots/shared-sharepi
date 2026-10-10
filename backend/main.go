//go:build !pentest

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/idtoken"
)

// googleVerifier validates a Google ID token: signature, issuer, expiry and
// audience (the publisher's Web client ID).
type googleVerifier struct{ audience string }

func (v googleVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	p, err := idtoken.Validate(ctx, token, v.audience)
	if err != nil {
		return Identity{}, err
	}
	email, _ := p.Claims["email"].(string)
	verified, _ := p.Claims["email_verified"].(bool)
	nonce, _ := p.Claims["nonce"].(string)
	return Identity{Sub: p.Subject, Email: email, EmailVerified: verified, Nonce: nonce}, nil
}

// newStorage builds the bucket and the URL signer for the cloud this instance
// runs on: STORAGE_PROVIDER is "gcs" (the default; Cloud Run) or "s3" (AWS,
// terraform/backend-aws). The container is the same image for both.
func newStorage(ctx context.Context, bucket string) (Store, Signer, error) {
	switch p := os.Getenv("STORAGE_PROVIDER"); p {
	case "", "gcs":
		gc, err := storage.NewClient(ctx)
		if err != nil {
			return nil, nil, err
		}
		b := gc.Bucket(bucket)
		return gcsStore{b}, gcsSigner{b: b, accessID: mustEnv("SIGNER_SERVICE_ACCOUNT")}, nil
	case "s3":
		// Region and credentials come from the environment (Lambda sets both).
		ac, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, nil, err
		}
		c := s3.NewFromConfig(ac)
		return s3Store{c: c, bucket: bucket}, s3Signer{p: s3.NewPresignClient(c), bucket: bucket}, nil
	default:
		return nil, nil, fmt.Errorf("STORAGE_PROVIDER must be gcs or s3, not %q", p)
	}
}

func main() {
	bucket := mustEnv("BUCKET")
	ctx := context.Background()
	store, signer, err := newStorage(ctx, bucket)
	if err != nil {
		log.Fatal(err)
	}

	cfg := Config{
		AdminSubs:        parseList(os.Getenv("ADMIN_SUBS"), false),
		AdminEmails:      parseList(os.Getenv("ADMIN_EMAILS"), true),
		PublicURL:        strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		DownloadTTL:      time.Hour,
		UploadTTL:        15 * time.Minute,
		MaxUploadSize:    5 << 30,
		MaxThumbnailSize: 1 << 20,
		MaxMediumSize:    16 << 20,
		MaxImageSize:     256 << 20,
		MaxEventItems:    9999,
		RosterTTL:        5 * time.Second,
		// Off until the app sends tokens bound to a backend (tokenBinding);
		// Terraform's require_token_binding turns it on.
		RequireTokenBinding: os.Getenv("REQUIRE_TOKEN_BINDING") == "true",
		// Unset (the default) disables all browser access; Terraform sets it
		// only when enable_web = true, to that Web service's own URL.
		WebOrigin: strings.TrimRight(os.Getenv("WEB_ORIGIN"), "/"),
	}
	log.Print(adminModeLog(cfg))
	srv := NewServer(cfg, googleVerifier{audience: mustEnv("GOOGLE_CLIENT_ID")}, store, signer)
	srv.checkForUpdateAsync(http.DefaultClient)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// The value comes from the environment: only a port number is accepted, so
	// nothing else can reach the log.
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		log.Fatal("PORT must be a port number")
	}
	log.Printf("listening on :%d", portNumber)
	// Photo bytes never pass through here, so every request is small and quick:
	// timeouts stop slow-client (slowloris) connections from holding instances.
	hs := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	log.Fatal(hs.ListenAndServe())
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}

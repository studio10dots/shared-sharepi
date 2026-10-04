//go:build !pentest

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
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
	return Identity{Sub: p.Subject, Email: email, EmailVerified: verified}, nil
}

func main() {
	bucket := mustEnv("BUCKET")
	ctx := context.Background()
	gc, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}
	b := gc.Bucket(bucket)

	admins := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			admins[e] = true
		}
	}
	srv := NewServer(Config{
		AdminEmails:      admins,
		PublicURL:        strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		DownloadTTL:      time.Hour,
		UploadTTL:        15 * time.Minute,
		MaxUploadSize:    5 << 30,
		MaxThumbnailSize: 1 << 20,
		MaxMediumSize:    16 << 20,
		MaxImageSize:     256 << 20,
		MaxEventItems:    9999,
		RosterTTL:        5 * time.Second,
		// Unset (the default) disables all browser access; Terraform sets it
		// only when enable_web = true, to that Web service's own URL.
		WebOrigin: strings.TrimRight(os.Getenv("WEB_ORIGIN"), "/"),
	}, googleVerifier{audience: mustEnv("GOOGLE_CLIENT_ID")}, gcsStore{b}, gcsSigner{b: b, accessID: mustEnv("SIGNER_SERVICE_ACCOUNT")})
	srv.checkForUpdateAsync(http.DefaultClient)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on :%s", port)
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

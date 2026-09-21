// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"crypto/tls"
	"errors"
	"os"
	"sync"
	"time"
)

// certKeeper serves the TLS certificate and picks up renewals without a
// restart.
//
// The certificate under an ingress is renewed by something else — lego on a
// timer, in the deployment this was built for — which rewrites the same two
// files every couple of months. A server that read them once at startup would
// keep presenting the old certificate until someone restarted it, and the
// symptom would arrive sixty days later as an expiry nobody was watching for.
//
// Restarting is not a neutral fix either: every tunnel drops, every published
// registry goes unreachable until its client reconnects. So the pair is re-read
// in place instead, on a stat of the certificate file rather than on a clock —
// a renewal changes the file, and nothing else has to be arranged.
type certKeeper struct {
	certPath, keyPath string

	mu       sync.RWMutex
	cert     *tls.Certificate
	mod      time.Time // certificate file's modification time when last loaded
	lastStat time.Time // when the file was last checked
}

// statInterval bounds how often the files are stat'ed. A handshake is not a
// good place to touch the filesystem, and a renewal that takes a few seconds to
// be noticed costs nothing.
const statInterval = 15 * time.Second

func newCertKeeper(certPath, keyPath string) (*certKeeper, error) {
	if certPath == "" || keyPath == "" {
		return nil, errors.New("both a certificate and a key are needed to serve TLS")
	}
	k := &certKeeper{certPath: certPath, keyPath: keyPath}
	if err := k.reload(); err != nil {
		return nil, err
	}
	return k, nil
}

// GetCertificate is the tls.Config hook. It reloads when the file on disk has
// changed, and on a failure keeps serving what it already has: a half-written
// certificate during a renewal is a moment to ride out, not a reason to start
// refusing connections.
func (k *certKeeper) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	k.mu.RLock()
	cert, mod, last := k.cert, k.mod, k.lastStat
	k.mu.RUnlock()

	if time.Since(last) >= statInterval {
		if st, err := os.Stat(k.certPath); err == nil && st.ModTime().After(mod) {
			_ = k.reload() // failure leaves the existing certificate in place
		} else {
			k.mu.Lock()
			k.lastStat = time.Now()
			k.mu.Unlock()
		}
		k.mu.RLock()
		cert = k.cert
		k.mu.RUnlock()
	}
	if cert == nil {
		return nil, errors.New("no certificate loaded")
	}
	return cert, nil
}

func (k *certKeeper) reload() error {
	cert, err := tls.LoadX509KeyPair(k.certPath, k.keyPath)
	if err != nil {
		return err
	}
	st, err := os.Stat(k.certPath)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.cert, k.mod, k.lastStat = &cert, st.ModTime(), time.Now()
	k.mu.Unlock()
	return nil
}

// NotAfter is when the loaded certificate expires, for the console and the
// startup line. Zero when nothing is loaded.
func (k *certKeeper) NotAfter() time.Time {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.cert == nil || k.cert.Leaf == nil {
		return time.Time{}
	}
	return k.cert.Leaf.NotAfter
}

// TLSConfig builds the server configuration, or nil when no certificate is
// configured — in which case the ingress serves plain HTTP, which is right for
// a laptop and wrong for anything else.
func (s *Server) TLSConfig() (*tls.Config, error) {
	if s.Cfg.TLSCert == "" && s.Cfg.TLSKey == "" {
		return nil, nil
	}
	k, err := newCertKeeper(s.Cfg.TLSCert, s.Cfg.TLSKey)
	if err != nil {
		return nil, err
	}
	s.certs = k
	return &tls.Config{
		GetCertificate: k.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
	}, nil
}

// CertNotAfter reports the serving certificate's expiry, zero when plain HTTP.
func (s *Server) CertNotAfter() time.Time {
	if s.certs == nil {
		return time.Time{}
	}
	return s.certs.NotAfter()
}

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MarsSemi/MarsCloud-SaaS/SDK/Security"
)

type clientAuthPayload struct {
	ClientID string `json:"client_id"`
	Purpose  string `json:"purpose"`
	IssuedAt int64  `json:"iat"`
	ExpireAt int64  `json:"exp"`
}

const defaultClientTokenTTLSec = 90 * 24 * 60 * 60

func encodeClientAuthToken(clientID string, ttlSec int) (string, time.Time, time.Time, error) {
	return encodeAuthToken(clientID, "smalltalk-client-auth", ttlSec)
}

func encodeSessionAuthToken(clientID string, ttlSec int) (string, time.Time, time.Time, error) {
	return encodeAuthToken(clientID, "smalltalk-session-auth", ttlSec)
}

func encodeAuthToken(clientID, purpose string, ttlSec int) (string, time.Time, time.Time, error) {
	if clientID == "" {
		return "", time.Time{}, time.Time{}, ErrMissingClientID
	}
	if strings.TrimSpace(purpose) == "" {
		return "", time.Time{}, time.Time{}, errors.New("missing auth token purpose")
	}
	if ttlSec <= 0 {
		ttlSec = defaultClientTokenTTLSec
	}

	pubKey, _, err := exportCurrentRSAKeys()
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}

	now := time.Now()
	exp := now.Add(time.Duration(ttlSec) * time.Second)
	payload := clientAuthPayload{
		ClientID: clientID,
		Purpose:  strings.TrimSpace(purpose),
		IssuedAt: now.Unix(),
		ExpireAt: exp.Unix(),
	}
	plain := []byte(compactAuthPayload(payload))
	cipherText, err := encryptWithCurrentRSA(pubKey, plain)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	return base64.RawURLEncoding.EncodeToString(cipherText), now, exp, nil
}

func decodeClientAuthToken(token string) (*clientAuthPayload, error) {
	if token == "" {
		return nil, errors.New("missing token")
	}

	_, priKey, err := exportCurrentRSAKeys()
	if err != nil {
		return nil, err
	}

	cipherText, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	plain, err := decryptWithCurrentRSA(priKey, cipherText)
	if err != nil {
		return nil, err
	}
	payload, err := parseCompactAuthPayload(string(plain))
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

func compactAuthPayload(payload clientAuthPayload) string {
	purpose := strings.TrimSpace(payload.Purpose)
	if purpose == "" {
		purpose = "smalltalk-client-auth"
	}
	return fmt.Sprintf("v2|%s|%s|%d|%d", purpose, payload.ClientID, payload.IssuedAt, payload.ExpireAt)
}

func parseCompactAuthPayload(raw string) (clientAuthPayload, error) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	switch {
	case len(parts) == 4 && parts[0] == "v1":
		iat, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return clientAuthPayload{}, err
		}
		exp, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return clientAuthPayload{}, err
		}
		return clientAuthPayload{
			ClientID: parts[1],
			Purpose:  "smalltalk-client-auth",
			IssuedAt: iat,
			ExpireAt: exp,
		}, nil
	case len(parts) == 5 && parts[0] == "v2":
	default:
		return clientAuthPayload{}, errors.New("invalid auth payload")
	}
	iat, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return clientAuthPayload{}, err
	}
	exp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return clientAuthPayload{}, err
	}
	return clientAuthPayload{
		ClientID: parts[2],
		Purpose:  parts[1],
		IssuedAt: iat,
		ExpireAt: exp,
	}, nil
}

func encryptWithCurrentRSA(pubKey *rsa.PublicKey, plain []byte) ([]byte, error) {
	if pubKey == nil {
		return nil, errors.New("missing public key")
	}
	if pubKey.Size() >= 128 {
		return rsa.EncryptOAEP(sha256.New(), rand.Reader, pubKey, plain, []byte("smalltalk-client-auth"))
	}
	return encryptLegacyPKCS1v15(pubKey, plain)
}

func decryptWithCurrentRSA(priKey *rsa.PrivateKey, cipherText []byte) ([]byte, error) {
	if priKey == nil {
		return nil, errors.New("missing private key")
	}
	if priKey.Size() >= 128 {
		if plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priKey, cipherText, []byte("smalltalk-client-auth")); err == nil {
			return plain, nil
		}
	}
	return decryptLegacyPKCS1v15(priKey, cipherText)
}

func encryptLegacyPKCS1v15(pubKey *rsa.PublicKey, plain []byte) ([]byte, error) {
	k := pubKey.Size()
	if len(plain) > k-11 {
		return nil, fmt.Errorf("auth payload too large for legacy rsa key (%d>%d)", len(plain), k-11)
	}

	em := make([]byte, k)
	em[0] = 0x00
	em[1] = 0x02
	ps := em[2 : k-len(plain)-1]
	if _, err := io.ReadFull(rand.Reader, ps); err != nil {
		return nil, err
	}
	for i := range ps {
		for ps[i] == 0x00 {
			if _, err := io.ReadFull(rand.Reader, ps[i:i+1]); err != nil {
				return nil, err
			}
		}
	}
	em[k-len(plain)-1] = 0x00
	copy(em[k-len(plain):], plain)

	m := new(big.Int).SetBytes(em)
	if m.Cmp(pubKey.N) >= 0 {
		return nil, errors.New("message too large")
	}

	e := big.NewInt(int64(pubKey.E))
	c := new(big.Int).Exp(m, e, pubKey.N)
	out := c.Bytes()
	if len(out) < k {
		padded := make([]byte, k)
		copy(padded[k-len(out):], out)
		out = padded
	}
	return out, nil
}

func decryptLegacyPKCS1v15(priKey *rsa.PrivateKey, cipherText []byte) ([]byte, error) {
	k := priKey.Size()
	if len(cipherText) != k {
		return nil, errors.New("invalid ciphertext size")
	}

	c := new(big.Int).SetBytes(cipherText)
	m := new(big.Int).Exp(c, priKey.D, priKey.N)
	em := m.Bytes()
	if len(em) < k {
		padded := make([]byte, k)
		copy(padded[k-len(em):], em)
		em = padded
	}
	if len(em) < 11 || em[0] != 0x00 || em[1] != 0x02 {
		return nil, errors.New("invalid legacy rsa padding")
	}
	sep := -1
	for i := 2; i < len(em); i++ {
		if em[i] == 0x00 {
			sep = i
			break
		}
	}
	if sep < 10 || sep >= len(em)-1 {
		return nil, errors.New("invalid legacy rsa separator")
	}
	return em[sep+1:], nil
}

func exportCurrentRSAKeys() (*rsa.PublicKey, *rsa.PrivateKey, error) {
	dir, err := os.MkdirTemp("", "smalltalk-rsa-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)

	pubPath := filepath.Join(dir, "pub.pem")
	priPath := filepath.Join(dir, "pri.pem")
	// MarsCloud 未連線時不一定會走過 SDK 的 RSA 初始化流程；
	// 本機 session 仍需要可持續使用的程序內金鑰，因此在首次使用時建立。
	if !Security.JWT.SaveRSAKey(pubPath, priPath) {
		if !Security.JWT.LoadRSAKey(nil, nil) || !Security.JWT.SaveRSAKey(pubPath, priPath) {
			return nil, nil, errors.New("rsa key export failed")
		}
	}

	pubKey, err := loadRSAPublicKey(pubPath)
	if err != nil {
		return nil, nil, err
	}
	priKey, err := loadRSAPrivateKey(priPath)
	if err != nil {
		return nil, nil, err
	}
	return pubKey, priKey, nil
}

func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("invalid public pem")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("invalid public key type")
	}
	return key, nil
}

func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("invalid private pem")
	}
	pri, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := pri.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("invalid private key type")
	}
	return key, nil
}

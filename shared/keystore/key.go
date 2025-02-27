// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// Modified by Prysmatic Labs 2018
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package keystore

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/shared/bls"
)

const (
	keyHeaderKDF = "scrypt"

	// StandardScryptN is the N parameter of Scrypt encryption algorithm, using 256MB
	// memory and taking approximately 1s CPU time on a modern processor.
	StandardScryptN = 1 << 18

	// StandardScryptP is the P parameter of Scrypt encryption algorithm, using 256MB
	// memory and taking approximately 1s CPU time on a modern processor.
	StandardScryptP = 1

	// LightScryptN is the N parameter of Scrypt encryption algorithm, using 4MB
	// memory and taking approximately 100ms CPU time on a modern processor.
	LightScryptN = 1 << 12

	// LightScryptP is the P parameter of Scrypt encryption algorithm, using 4MB
	// memory and taking approximately 100ms CPU time on a modern processor.
	LightScryptP = 6

	scryptR     = 8
	scryptDKLen = 32
)

// Key is the object that stores all the user data related to their public/secret keys.
type Key struct {
	ID uuid.UUID // Version 4 "random" for unique id not derived from key data

	PublicKey *bls.PublicKey // Represents the public key of the user.

	SecretKey *bls.SecretKey // Represents the private key of the user.
}

type keyStore interface {
	// Loads and decrypts the key from disk.
	GetKey(filename string, password string) (*Key, error)
	// Writes and encrypts the key.
	StoreKey(filename string, k *Key, auth string) error
	// Joins filename with the key directory unless it is already absolute.
	JoinPath(filename string) string
}

type plainKeyJSON struct {
	PublicKey string `json:"address"`
	SecretKey string `json:"privatekey"`
	ID        string `json:"id"`
}

type encryptedKeyJSON struct {
	PublicKey string     `json:"publickey"`
	Crypto    cryptoJSON `json:"crypto"`
	ID        string     `json:"id"`
}

type cryptoJSON struct {
	Cipher       string                 `json:"cipher"`
	CipherText   string                 `json:"ciphertext"`
	CipherParams cipherparamsJSON       `json:"cipherparams"`
	KDF          string                 `json:"kdf"`
	KDFParams    map[string]interface{} `json:"kdfparams"`
	MAC          string                 `json:"mac"`
}

type cipherparamsJSON struct {
	IV string `json:"iv"`
}

// MarshalJSON marshalls a key struct into a JSON blob.
func (k *Key) MarshalJSON() (j []byte, err error) {
	jStruct := plainKeyJSON{
		hex.EncodeToString(k.PublicKey.Marshal()),
		hex.EncodeToString(k.SecretKey.Marshal()),
		k.ID.String(),
	}
	j, err = json.Marshal(jStruct)
	return j, err
}

// UnmarshalJSON unmarshals a blob into a key struct.
func (k *Key) UnmarshalJSON(j []byte) (err error) {
	keyJSON := new(plainKeyJSON)
	err = json.Unmarshal(j, &keyJSON)
	if err != nil {
		return err
	}

	u := new(uuid.UUID)
	*u = uuid.Parse(keyJSON.ID)
	k.ID = *u
	pubkey, err := hex.DecodeString(keyJSON.PublicKey)
	if err != nil {
		return err
	}
	seckey, err := hex.DecodeString(keyJSON.SecretKey)
	if err != nil {
		return err
	}

	k.PublicKey, err = bls.PublicKeyFromBytes(pubkey)
	if err != nil {
		return err
	}
	k.SecretKey, err = bls.SecretKeyFromBytes(seckey)
	if err != nil {
		return err
	}
	return nil
}

func newKeyFromBLS(blsKey *bls.SecretKey) (*Key, error) {
	id := uuid.NewRandom()
	pubkey := blsKey.PublicKey()
	key := &Key{
		ID:        id,
		PublicKey: pubkey,
		SecretKey: blsKey,
	}
	return key, nil
}

// NewKey generates a new random key.
func NewKey(rand io.Reader) (*Key, error) {
	secretKey, err := bls.RandKey(rand)
	if err != nil {
		return nil, errors.Wrap(err, "could not generate random key")
	}
	return newKeyFromBLS(secretKey)
}

func storeNewRandomKey(ks keyStore, rand io.Reader, password string) error {
	key, err := NewKey(rand)
	if err != nil {
		return err
	}
	if err := ks.StoreKey(ks.JoinPath(keyFileName(key.PublicKey)), key, password); err != nil {
		return err
	}
	return nil
}

func writeKeyFile(file string, content []byte) error {
	// Create the keystore directory with appropriate permissions
	// in case it is not present yet.
	const dirPerm = 0700
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		return err
	}
	// Atomic write: create a temporary hidden file first
	// then move it into place. TempFile assigns mode 0600.
	f, err := ioutil.TempFile(filepath.Dir(file), "."+filepath.Base(file)+".tmp")
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		newErr := f.Close()
		if newErr != nil {
			err = newErr
		}
		newErr = os.Remove(f.Name())
		if newErr != nil {
			err = newErr
		}
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}

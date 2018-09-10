package lib

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/omniledger/darc"
	ol "github.com/dedis/cothority/omniledger/service"
	"github.com/dedis/cothority/skipchain"
	"github.com/dedis/onet"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
)

var ConfigPath = "."

type Config struct {
	Roster        onet.Roster
	OmniledgerID  skipchain.SkipBlockID
	GenesisDarc   darc.Darc
	AdminIdentity darc.Identity
}

func LoadKey(id darc.Identity) (*darc.Signer, error) {
	// Find private key file.
	fn := fmt.Sprintf("key-%s.cfg", id)
	fn = filepath.Join(ConfigPath, fn)
	return LoadSigner(fn)
}

func LoadSigner(fn string) (*darc.Signer, error) {
	buf, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}

	var signer darc.Signer
	err = protobuf.DecodeWithConstructors(buf, &signer,
		network.DefaultConstructors(cothority.Suite))
	return &signer, err
}

func SaveKey(signer darc.Signer) error {
	os.MkdirAll(ConfigPath, 0755)

	fn := fmt.Sprintf("key-%s.cfg", signer.Identity())
	fn = filepath.Join(ConfigPath, fn)

	// perms = 0400 because there is key material inside this file.
	f, err := os.OpenFile(fn, os.O_RDWR|os.O_CREATE, 0400)
	if err != nil {
		return fmt.Errorf("could not write %v: %v", fn, err)
	}

	buf, err := protobuf.Encode(&signer)
	if err != nil {
		return err
	}
	_, err = f.Write(buf)
	if err != nil {
		return err
	}
	return f.Close()
}

func SaveConfig(cfg Config) (string, error) {
	os.MkdirAll(ConfigPath, 0755)

	fn := fmt.Sprintf("ol-%x.cfg", cfg.OmniledgerID)
	fn = filepath.Join(ConfigPath, fn)

	buf, err := protobuf.Encode(&cfg)
	if err != nil {
		return fn, err
	}
	err = ioutil.WriteFile(fn, buf, 0644)
	if err != nil {
		return fn, err
	}

	return fn, nil
}

func LoadConfig(file string) (cfg Config, cl *ol.Client, err error) {
	var cfgBuf []byte
	cfgBuf, err = ioutil.ReadFile(file)
	if err != nil {
		return
	}
	err = protobuf.DecodeWithConstructors(cfgBuf, &cfg,
		network.DefaultConstructors(cothority.Suite))
	if err != nil {
		return
	}
	cl = ol.NewClient(cfg.OmniledgerID, cfg.Roster)
	return
}

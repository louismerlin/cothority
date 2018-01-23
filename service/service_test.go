package service

import (
	"testing"

	bolt "github.com/coreos/bbolt"
	"github.com/dedis/cothority"
	"github.com/dedis/cothority/skipchain"
	"github.com/dedis/kyber/suites"
	"github.com/dedis/onchain-secrets"
	"github.com/dedis/onchain-secrets/darc"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"
	"github.com/stretchr/testify/require"
)

var tSuite = suites.MustFind("Ed25519")

func TestMain(m *testing.M) {
	log.MainTest(m, 2)
}

func TestService_proof(t *testing.T) {
	o := createOCS(t)
	defer o.local.CloseAll()

	// Creating a write request
	encKey := []byte{1, 2, 3}
	write := ocs.NewWrite(cothority.Suite, o.sc.OCS.Hash, o.sc.X, o.readers, encKey)
	write.Data = []byte{}
	sigPath := darc.NewSignaturePath([]*darc.Darc{o.readers}, *o.writerI, darc.User)
	sig, err := darc.NewDarcSignature(write.Reader.GetID(), sigPath, o.writerS)
	require.Nil(t, err)
	wr, err := o.service.WriteRequest(&ocs.WriteRequest{
		OCS:       o.sc.OCS.Hash,
		Write:     *write,
		Signature: *sig,
		Readers:   o.readers,
	})
	require.Nil(t, err)

	// Making a read request
	sigRead, err := darc.NewDarcSignature(wr.SB.Hash, sigPath, o.writerS)
	require.Nil(t, err)
	read := ocs.Read{
		DataID:    wr.SB.Hash,
		Signature: *sigRead,
	}
	rr, err := o.service.ReadRequest(&ocs.ReadRequest{
		OCS:  o.sc.OCS.Hash,
		Read: read,
	})
	require.Nil(t, err)

	// Decoding the file
	symEnc, err := o.service.DecryptKeyRequest(&ocs.DecryptKeyRequest{
		Read: rr.SB.Hash,
	})
	require.Nil(t, err)
	sym, err2 := ocs.DecodeKey(cothority.Suite, o.sc.X, write.Cs, symEnc.XhatEnc, o.writer.Secret)
	require.Nil(t, err2)
	require.Equal(t, encKey, sym)

	// Create a wrong Decryption request by abusing skipchain's database and
	// writing a wrong reader public key to the OCS-data.
	ocsd := ocs.NewOCS(rr.SB.Data)
	ocsd.Read.Signature.SignaturePath.Signer.Ed25519.Point = cothority.Suite.Point()
	rr.SB.Data, err = protobuf.Encode(ocsd)
	require.Nil(t, err)
	val, err := network.Marshal(rr.SB)
	require.Nil(t, err)
	bucket := skipchain.ServiceName + "_skipblocks"
	for _, s := range o.services {
		db := s.(*Service).db()
		require.Nil(t, db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte(bucket)).Put(rr.SB.Hash, val)
		}))
	}
	symEnc, err = o.service.DecryptKeyRequest(&ocs.DecryptKeyRequest{
		Read: rr.SB.Hash,
	})
	require.NotNil(t, err)

	// GetReadRequests
	requests, err := o.service.GetReadRequests(&ocs.GetReadRequests{
		Start: wr.SB.Hash,
		Count: 0,
	})
	require.Nil(t, err)
	require.Equal(t, 1, len(requests.Documents))
}

func TestService_GetDarcPath(t *testing.T) {
	o := createOCS(t)
	defer o.local.CloseAll()

	w := &darc.Darc{}
	wDarcID := darc.NewDarcIdentity(w.GetID())
	wID, err := darc.NewIdentity(wDarcID, nil)

	newReader := o.readers.Copy()
	require.Nil(t, err)
	newReader.AddUser(wID)
	path := darc.NewSignaturePath([]*darc.Darc{o.readers}, *o.writerI, darc.Owner)
	err = newReader.SetEvolution(o.readers, path, &darc.Signer{Ed25519: o.writer})
	require.Nil(t, err)

	_, cerr := o.service.UpdateDarc(&ocs.UpdateDarc{
		OCS:  o.sc.OCS.SkipChainID(),
		Darc: *w,
	})
	require.Nil(t, cerr)
	_, cerr = o.service.UpdateDarc(&ocs.UpdateDarc{
		OCS:  o.sc.OCS.SkipChainID(),
		Darc: *newReader,
	})
	require.Nil(t, cerr)

	request := &ocs.GetDarcPath{
		OCS:        o.sc.OCS.SkipChainID(),
		BaseDarcID: o.readers.GetID(),
		Identity:   *wID,
		Role:       int(darc.Owner),
	}

	log.Lvl1("Searching for wrong role")
	reply, cerr := o.service.GetDarcPath(request)
	require.NotNil(t, cerr)

	log.Lvl1("Searching for correct role")
	request.Role = int(darc.User)
	reply, cerr = o.service.GetDarcPath(request)
	require.Nil(t, cerr)
	require.NotNil(t, reply.Path)
	require.NotEqual(t, 0, len(*reply.Path))
}

type ocsStruct struct {
	local    *onet.LocalTest
	services []onet.Service
	service  *Service
	writer   *darc.Ed25519Signer
	writerS  *darc.Signer
	writerI  *darc.Identity
	readers  *darc.Darc
	sc       *ocs.CreateSkipchainsReply
}

func createOCS(t *testing.T) *ocsStruct {
	o := &ocsStruct{
		local: onet.NewTCPTest(tSuite),
	}
	// generate 5 hosts, they don't connect, they process messages, and they
	// don't register the tree or entitylist
	hosts, roster, _ := o.local.GenTree(10, true)

	o.services = o.local.GetServices(hosts, templateID)
	o.service = o.services[0].(*Service)

	// Initializing onchain-secrets skipchain
	o.writer = darc.NewEd25519Signer(nil, nil)
	o.writerS = &darc.Signer{Ed25519: o.writer}
	var err error
	o.writerI, err = darc.NewIdentity(nil, darc.NewEd25519Identity(o.writer.Point))
	require.Nil(t, err)
	o.readers = darc.NewDarc(nil, nil, nil)
	o.readers.AddOwner(o.writerI)
	o.readers.AddUser(o.writerI)
	o.sc, err = o.service.CreateSkipchains(&ocs.CreateSkipchainsRequest{
		Roster:  *roster,
		Writers: *o.readers,
	})
	require.Nil(t, err)
	return o
}

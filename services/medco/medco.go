package medco_service

import (
	"github.com/btcsuite/goleveldb/leveldb/errors"
	"github.com/dedis/cothority/lib/dbg"
	"github.com/dedis/cothority/lib/network"
	"github.com/dedis/cothority/lib/sda"
	"github.com/dedis/cothority/protocols/medco"
	. "github.com/dedis/cothority/services/medco/structs"
	"github.com/dedis/crypto/random"
	"github.com/satori/go.uuid"
)

const MEDCO_SERVICE_NAME = "MedCo"

func init() {
	sda.RegisterNewService(MEDCO_SERVICE_NAME, NewMedcoService)
	network.RegisterMessageType(&ClientResponse{})
	network.RegisterMessageType(&SurveyResultsQuery{})
	network.RegisterMessageType(&SurveyCreationQuery{})
	network.RegisterMessageType(&SurveyResultResponse{})
	network.RegisterMessageType(&ServiceResponse{})
}

type MedcoService struct {
	*sda.ServiceProcessor
	homePath string

	surveys  map[SurveyID]Survey
	currentSurveyID SurveyID
}

func NewMedcoService(c sda.Context, path string) sda.Service {
	newMedCoInstance := &MedcoService{
		ServiceProcessor: sda.NewServiceProcessor(c),
		homePath:         path,
	}
	newMedCoInstance.RegisterMessage(newMedCoInstance.HandleSurveyResponseData)
	newMedCoInstance.RegisterMessage(newMedCoInstance.HandleSurveyResultsQuery)
	newMedCoInstance.RegisterMessage(newMedCoInstance.HandleSurveyCreationQuery)
	return newMedCoInstance
}

func (mcs *MedcoService) HandleSurveyCreationQuery(e *network.Entity, recq *SurveyCreationQuery) (network.ProtocolMessage, error) {

	dbg.Lvl1(mcs.Entity(), "received a Survey Creation Query")
	if recq.SurveyID == nil {
		newID := SurveyID(uuid.NewV4().String())
		recq.SurveyID = &newID
		msg, _ := sda.CreateServiceMessage(MEDCO_SERVICE_NAME, recq)
		mcs.SendISMOthers(&recq.EntityList, msg)
		dbg.Lvl1(mcs.Entity(), "initiated the survey", newID)
	}

	mcs.surveys[*recq.SurveyID] =  Survey{
		SurveyStore: NewSurveyStore(),
		ID: *recq.SurveyID,
		EntityList: recq.EntityList,
		SurveyPHKey: network.Suite.Secret().Pick(random.Stream),
		ClientPublic: nil,
		SurveyDescription: recq.SurveyDescription,
	}

	dbg.Lvl1(mcs.Entity(), "created the survey", *recq.SurveyID)

	return &ServiceResponse{*recq.SurveyID}, nil
}

func (mcs *MedcoService) HandleSurveyResponseData(e *network.Entity, resp *SurveyResponseQuery) (network.ProtocolMessage, error) {

	mcs.surveys[resp.SurveyID].InsertClientResponse(resp.ClientResponse)
	dbg.Lvl1(mcs.Entity(), "recieved survey response data from ", e)
	return &ServiceResponse{"1"}, nil
}

func (mcs *MedcoService) HandleSurveyResultsQuery(e *network.Entity, resq *SurveyResultsQuery) (network.ProtocolMessage, error) {

	dbg.Lvl1(mcs.Entity(), "recieved a survey result query from", e)
	survey := mcs.surveys[resq.SurveyID]
	survey.ClientPublic = resq.ClientPublic
	pi,_ := mcs.startProtocol(medco.MEDCO_SERVICE_PROTOCOL_NAME, resq.SurveyID)

	<- pi.(*medco.MedcoServiceProtocol).FeedbackChannel
	dbg.Lvl1(mcs.Entity(), "completed the query processing...")
	return &SurveyResultResponse{mcs.surveys[resq.SurveyID].PollDeliverableResults()}, nil
}

func (mcs *MedcoService) NewProtocol(tn *sda.TreeNodeInstance, conf *sda.GenericConfig) (sda.ProtocolInstance, error) {
	// Observation : which data we operate the protocol on is important only for aggreg and there is no ambiguity
	// for those data (we aggregate everything that is ready to be aggregated). For key switching, this is a
	// problem as we need to know from which key to which key we switch. The current best solution seems to be make
	// two versions of the key switching protocol because it also solves the interface unmarshalling problem.
	var pi sda.ProtocolInstance
	var err error
	targetSurveyID := mcs.currentSurveyID
	targetSurvey := mcs.surveys[targetSurveyID]
	switch tn.ProtocolName() {
	case medco.MEDCO_SERVICE_PROTOCOL_NAME:
		pi, err = medco.NewMedcoServiceProcotol(tn)
		medcoServ := pi.(*medco.MedcoServiceProtocol)
		medcoServ.MedcoServiceInstance = mcs
		medcoServ.TargetSurvey = &targetSurveyID
	case medco.DETERMINISTIC_SWITCHING_PROTOCOL_NAME:
		pi, err = medco.NewDeterministSwitchingProtocol(tn)
		detSwitch := pi.(*medco.DeterministicSwitchingProtocol)
		detSwitch.SurveyPHKey = &targetSurvey.SurveyPHKey
		if tn.IsRoot() {
			groupingAttr := targetSurvey.PollProbabilisticGroupingAttributes()
			detSwitch.TargetOfSwitch = &groupingAttr
		}
	case medco.PRIVATE_AGGREGATE_PROTOCOL_NAME:
		pi, err = medco.NewPrivateAggregate(tn)
		groups, groupedData := targetSurvey.PollLocallyAggregatedResponses()
		pi.(*medco.PrivateAggregateProtocol).GroupedData = &groupedData
		pi.(*medco.PrivateAggregateProtocol).Groups = &groups
	case medco.PROBABILISTIC_SWITCHING_PROTOCOL_NAME:
		pi, err = medco.NewProbabilisticSwitchingProtocol(tn)
		probSwitch := pi.(*medco.ProbabilisticSwitchingProtocol)
		probSwitch.SurveyPHKey = &targetSurvey.SurveyPHKey
		if tn.IsRoot() {
			groups := targetSurvey.PollCothorityAggregatedGroupsId()
			probSwitch.TargetOfSwitch  = GroupingAttributesToDeterministicCipherVector(&groups)
			probSwitch.TargetPublicKey = &targetSurvey.ClientPublic
		}
	case medco.KEY_SWITCHING_PROTOCOL_NAME:
		pi, err = medco.NewKeySwitchingProtocol(tn)
		keySwitch := pi.(*medco.KeySwitchingProtocol)
		if tn.IsRoot() {
			coaggr := targetSurvey.PollCothorityAggregatedGroupsAttr()
			keySwitch.TargetOfSwitch = &coaggr
			keySwitch.TargetPublicKey = &targetSurvey.ClientPublic
		}
	default:
		return nil, errors.New("Service attempts to start an unknown protocol: " + tn.ProtocolName() + ".")
	}
	return pi, err
}

func (mcs *MedcoService) startProtocol(name string, targetSurvey SurveyID) (sda.ProtocolInstance, error) {
	survey := mcs.surveys[targetSurvey]
	tree := survey.EntityList.GenerateNaryTreeWithRoot(2, mcs.Entity())
	tni := mcs.NewTreeNodeInstance(tree, tree.Root, name)
	mcs.currentSurveyID = targetSurvey
	pi , err := mcs.NewProtocol(tni, nil)
	mcs.RegisterProtocolInstance(pi)
	go pi.Dispatch()
	go pi.Start()
	return pi, err
}

// Pipeline steps forward operations

// Performs the private grouping on the currently collected data
func (mcs *MedcoService) FlushCollectedData(targetSurvey SurveyID) error {

	// TODO: Start only if data
	pi, err := mcs.startProtocol(medco.DETERMINISTIC_SWITCHING_PROTOCOL_NAME, targetSurvey)
	if err != nil {
		return err
	}
	deterministicSwitchedResult := <-pi.(*medco.DeterministicSwitchingProtocol).FeedbackChannel

	mcs.surveys[targetSurvey].PushDeterministicGroupingAttributes(*DeterministicCipherVectorToGroupingAttributes(&deterministicSwitchedResult))
	return err
}

// Performs the per-group aggregation on the currently grouped data
func (mcs *MedcoService) FlushGroupedData(targetSurvey SurveyID) error {

	pi, err := mcs.startProtocol(medco.PRIVATE_AGGREGATE_PROTOCOL_NAME, targetSurvey)
	if err != nil {
		return err
	}
	cothorityAggregatedData := <-pi.(*medco.PrivateAggregateProtocol).FeedbackChannel

	mcs.surveys[targetSurvey].PushCothorityAggregatedGroups(cothorityAggregatedData.Groups, cothorityAggregatedData.GroupedData)

	return err
}

// Perform the switch to data querier key on the currently aggregated data
func (mcs *MedcoService) FlushAggregatedData(targetSurvey SurveyID) error {

	pi, err := mcs.startProtocol(medco.KEY_SWITCHING_PROTOCOL_NAME, targetSurvey)
	if err != nil {
		return err
	}
	keySwitchedAggregatedAttributes := <-pi.(*medco.KeySwitchingProtocol).FeedbackChannel


	pi, err = mcs.startProtocol(medco.PROBABILISTIC_SWITCHING_PROTOCOL_NAME, targetSurvey)
	if err != nil {
		return err
	}
	keySwitchedAggregatedGroups := <-pi.(*medco.ProbabilisticSwitchingProtocol).FeedbackChannel

	mcs.surveys[targetSurvey].PushQuerierKeyEncryptedData(keySwitchedAggregatedGroups, keySwitchedAggregatedAttributes)

	return err
}

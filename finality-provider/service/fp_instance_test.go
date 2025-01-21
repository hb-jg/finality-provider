package service_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/babylonlabs-io/babylon/testutil/datagen"
	bbntypes "github.com/babylonlabs-io/babylon/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"github.com/babylonlabs-io/finality-provider/clientcontroller/api"
	"github.com/babylonlabs-io/finality-provider/eotsmanager"
	eotscfg "github.com/babylonlabs-io/finality-provider/eotsmanager/config"
	"github.com/babylonlabs-io/finality-provider/finality-provider/config"
	"github.com/babylonlabs-io/finality-provider/finality-provider/service"
	fpkr "github.com/babylonlabs-io/finality-provider/keyring"
	"github.com/babylonlabs-io/finality-provider/metrics"
	"github.com/babylonlabs-io/finality-provider/testutil"
	"github.com/babylonlabs-io/finality-provider/types"
)

func FuzzCommitPubRandList(f *testing.F) {
	testutil.AddRandomSeedsToFuzzer(f, 10)
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))

		randomStartingHeight := uint64(r.Int63n(100) + 1)
		currentHeight := randomStartingHeight + uint64(r.Int63n(10)+2)
		startingBlock := &types.BlockInfo{Height: randomStartingHeight, Hash: testutil.GenRandomByteArray(r, 32)}
		mockBabylonController := testutil.PrepareMockedBabylonController(t)
		expectedTxHash := testutil.GenRandomHexStr(r, 32)
		mockConsumerController := testutil.PrepareMockedConsumerControllerWithTxHash(t, r, randomStartingHeight, currentHeight, expectedTxHash)
		mockConsumerController.EXPECT().QueryFinalityProviderHasPower(gomock.Any(), gomock.Any()).
			Return(false, nil).AnyTimes()
		_, fpIns, cleanUp := startFinalityProviderAppWithRegisteredFp(t, r, mockBabylonController, mockConsumerController, true, randomStartingHeight, testutil.TestPubRandNum)
		defer cleanUp()

		res, err := fpIns.CommitPubRand(startingBlock.Height)
		require.NoError(t, err)
		require.Equal(t, expectedTxHash, res.TxHash)
	})
}

func FuzzSubmitFinalitySigs(f *testing.F) {
	testutil.AddRandomSeedsToFuzzer(f, 10)
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))

		randomStartingHeight := uint64(r.Int63n(100) + 1)
		currentHeight := randomStartingHeight + uint64(r.Int63n(10)+1)
		startingBlock := &types.BlockInfo{Height: randomStartingHeight, Hash: testutil.GenRandomByteArray(r, 32)}
		mockBabylonController := testutil.PrepareMockedBabylonController(t)
		mockConsumerController := testutil.PrepareMockedConsumerController(t, r, randomStartingHeight, currentHeight)
		mockConsumerController.EXPECT().QueryLatestBlockHeight().Return(uint64(0), nil).AnyTimes()
		_, fpIns, cleanUp := startFinalityProviderAppWithRegisteredFp(t, r, mockBabylonController, mockConsumerController, true, randomStartingHeight, testutil.TestPubRandNum)
		defer cleanUp()

		// commit pub rand
		_, err := fpIns.CommitPubRand(startingBlock.Height)
		require.NoError(t, err)

		// mock committed pub rand
		lastCommittedHeight := randomStartingHeight + 25
		lastCommittedPubRand := &types.PubRandCommit{
			StartHeight: lastCommittedHeight,
			NumPubRand:  1000,
			Commitment:  datagen.GenRandomByteArray(r, 32),
		}
		mockConsumerController.EXPECT().QueryLastPublicRandCommit(gomock.Any()).Return(lastCommittedPubRand, nil).AnyTimes()
		// mock voting power and commit pub rand
		mockConsumerController.EXPECT().QueryFinalityProviderHasPower(fpIns.GetBtcPk(), gomock.Any()).
			Return(true, nil).AnyTimes()

		// submit finality sig
		nextBlock := &types.BlockInfo{
			Height: startingBlock.Height + 1,
			Hash:   testutil.GenRandomByteArray(r, 32),
		}
		expectedTxHash := testutil.GenRandomHexStr(r, 32)
		mockConsumerController.EXPECT().
			SubmitBatchFinalitySigs(fpIns.GetBtcPk(), []*types.BlockInfo{nextBlock}, gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&types.TxResponse{TxHash: expectedTxHash}, nil).AnyTimes()
		providerRes, err := fpIns.SubmitBatchFinalitySignatures([]*types.BlockInfo{nextBlock})
		require.NoError(t, err)
		require.Equal(t, expectedTxHash, providerRes.TxHash)

		// check the last_voted_height
		require.Equal(t, nextBlock.Height, fpIns.GetLastVotedHeight())
	})
}

func FuzzDetermineStartHeight(f *testing.F) {
	testutil.AddRandomSeedsToFuzzer(f, 10)
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))

		// generate random heights
		finalityActivationHeight := uint64(r.Int63n(1000) + 1)
		lastVotedHeight := uint64(r.Int63n(1000))
		highestVotedHeight := uint64(r.Int63n(1000))
		lastFinalizedHeight := uint64(r.Int63n(1000) + 1)

		randomStartingHeight := uint64(r.Int63n(100) + 1)
		currentHeight := randomStartingHeight + uint64(r.Int63n(10)+2)
		mockBabylonController := testutil.PrepareMockedBabylonController(t)
		mockConsumerController := testutil.PrepareMockedConsumerController(t, r, randomStartingHeight, currentHeight)

		// setup mocks
		mockConsumerController.EXPECT().QueryFinalityActivationBlockHeight().Return(finalityActivationHeight, nil).AnyTimes()
		mockConsumerController.EXPECT().
			QueryFinalityProviderHighestVotedHeight(gomock.Any()).
			Return(highestVotedHeight, nil).
			AnyTimes()
		finalizedBlock := &types.BlockInfo{
			Height: lastFinalizedHeight,
		}
		mockConsumerController.EXPECT().QueryLatestFinalizedBlock().Return(finalizedBlock, nil).AnyTimes()

		_, fpIns, cleanUp := startFinalityProviderAppWithRegisteredFp(t, r, mockBabylonController, mockConsumerController, false, randomStartingHeight, testutil.TestPubRandNum)
		defer cleanUp()
		fpIns.MustUpdateStateAfterFinalitySigSubmission(lastVotedHeight)

		startHeight, err := fpIns.DetermineStartHeight()
		require.NoError(t, err)

		require.Equal(t, startHeight, max(finalityActivationHeight, highestVotedHeight+1, lastFinalizedHeight+1, lastVotedHeight+1))
	})
}

func startFinalityProviderAppWithRegisteredFp(
	t *testing.T,
	r *rand.Rand,
	cc api.ClientController,
	consumerCon api.ConsumerController,
	isStaticStartHeight bool,
	startingHeight uint64,
	numPubRand uint32,
) (*service.FinalityProviderApp, *service.FinalityProviderInstance, func()) {
	logger := testutil.GetTestLogger(t)
	// create an EOTS manager
	eotsHomeDir := filepath.Join(t.TempDir(), "eots-home")
	eotsCfg := eotscfg.DefaultConfigWithHomePath(eotsHomeDir)
	eotsdb, err := eotsCfg.DatabaseConfig.GetDBBackend()
	require.NoError(t, err)
	em, err := eotsmanager.NewLocalEOTSManager(eotsHomeDir, eotsCfg.KeyringBackend, eotsdb, logger)
	require.NoError(t, err)

	// create finality-provider app with randomized config
	fpHomeDir := filepath.Join(t.TempDir(), "fp-home")
	fpCfg := config.DefaultConfigWithHome(fpHomeDir)
	fpCfg.NumPubRand = numPubRand
	fpCfg.PollerConfig.AutoChainScanningMode = !isStaticStartHeight
	fpCfg.PollerConfig.StaticChainScanningStartHeight = startingHeight
	db, err := fpCfg.DatabaseConfig.GetDBBackend()
	require.NoError(t, err)
	app, err := service.NewFinalityProviderApp(&fpCfg, cc, consumerCon, em, db, logger)
	require.NoError(t, err)
	err = app.Start()
	require.NoError(t, err)

	// create registered finality-provider
	eotsKeyName := testutil.GenRandomHexStr(r, 4)
	require.NoError(t, err)
	eotsPkBz, err := em.CreateKey(eotsKeyName, passphrase, hdPath)
	require.NoError(t, err)
	eotsPk, err := bbntypes.NewBIP340PubKey(eotsPkBz)
	require.NoError(t, err)
	pubRandProofStore := app.GetPubRandProofStore()
	fpStore := app.GetFinalityProviderStore()
	keyName := datagen.GenRandomHexStr(r, 10)
	chainID := datagen.GenRandomHexStr(r, 10)
	input := strings.NewReader("")
	kr, err := fpkr.CreateKeyring(
		fpCfg.BabylonConfig.KeyDirectory,
		fpCfg.BabylonConfig.ChainID,
		fpCfg.BabylonConfig.KeyringBackend,
		input,
	)
	require.NoError(t, err)
	kc, err := fpkr.NewChainKeyringControllerWithKeyring(kr, keyName, input)
	require.NoError(t, err)
	keyInfo, err := kc.CreateChainKey("", "", "")
	require.NoError(t, err)
	fpAddr := keyInfo.AccAddress
	err = fpStore.CreateFinalityProvider(
		fpAddr,
		eotsPk.MustToBTCPK(),
		testutil.RandomDescription(r),
		testutil.ZeroCommissionRate(),
		chainID,
	)
	require.NoError(t, err)
	m := metrics.NewFpMetrics()
	fpIns, err := service.NewFinalityProviderInstance(eotsPk, &fpCfg, fpStore, pubRandProofStore, cc, consumerCon, em, m, passphrase, make(chan *service.CriticalError), logger)
	require.NoError(t, err)

	cleanUp := func() {
		err = app.Stop()
		require.NoError(t, err)
		err = eotsdb.Close()
		require.NoError(t, err)
		err = db.Close()
		require.NoError(t, err)
		err = os.RemoveAll(eotsHomeDir)
		require.NoError(t, err)
		err = os.RemoveAll(fpHomeDir)
		require.NoError(t, err)
	}

	return app, fpIns, cleanUp
}

func setupBenchmarkEnvironment(t *testing.T, seed int64, numPubRand uint32) (*types.BlockInfo, *service.FinalityProviderInstance, func()) {
	r := rand.New(rand.NewSource(seed))

	randomStartingHeight := uint64(r.Int63n(100) + 1)
	currentHeight := randomStartingHeight + uint64(r.Int63n(10)+2)
	startingBlock := &types.BlockInfo{
		Height: randomStartingHeight,
		Hash:   testutil.GenRandomByteArray(r, 32),
	}

	// Mock client controller setup
	mockBabylonController := testutil.PrepareMockedBabylonController(t)
	mockConsumerController := testutil.PrepareMockedConsumerController(t, r, randomStartingHeight, currentHeight)
	mockConsumerController.EXPECT().QueryFinalityProviderHasPower(gomock.Any(), gomock.Any()).
		Return(false, nil).AnyTimes()

	// Set up finality provider app
	_, fpIns, cleanUp := startFinalityProviderAppWithRegisteredFp(t, r, mockBabylonController, mockConsumerController, true, randomStartingHeight, numPubRand)

	// Configure additional mocks
	expectedTxHash := testutil.GenRandomHexStr(r, 32)
	mockConsumerController.EXPECT().
		CommitPubRandList(fpIns.GetBtcPk(), startingBlock.Height+1, gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&types.TxResponse{TxHash: expectedTxHash}, nil).AnyTimes()
	mockConsumerController.EXPECT().QueryLastPublicRandCommit(gomock.Any()).Return(nil, nil).AnyTimes()

	return startingBlock, fpIns, cleanUp
}

func BenchmarkCommitPubRand(b *testing.B) {
	for _, numPubRand := range []uint32{10, 50, 100, 200, 500, 1000, 5000, 10000, 25000, 50000, 75000, 100000} {
		b.Run(fmt.Sprintf("numPubRand=%d", numPubRand), func(b *testing.B) {
			t := &testing.T{}
			startingBlock, fpIns, cleanUp := setupBenchmarkEnvironment(t, 42, numPubRand)
			defer cleanUp()

			// exclude setup time
			b.ResetTimer()

			var totalTiming service.CommitPubRandTiming
			for i := 0; i < b.N; i++ {
				res, timing, err := fpIns.HelperCommitPubRand(startingBlock.Height)
				if err != nil {
					b.Fatalf("unexpected error: %v", err)
				}

				if res == nil {
					b.Fatalf("unexpected result")
				}
				// Accumulate timings for averaging
				totalTiming.GetPubRandListTime += timing.GetPubRandListTime
				totalTiming.AddPubRandProofListTime += timing.AddPubRandProofListTime
				totalTiming.CommitPubRandListTime += timing.CommitPubRandListTime
			}
			b.ReportMetric(float64(totalTiming.GetPubRandListTime.Nanoseconds())/float64(b.N), "ns/GetPubRandList")
			b.ReportMetric(float64(totalTiming.AddPubRandProofListTime.Nanoseconds())/float64(b.N), "ns/AddPubRandProofList")
			b.ReportMetric(float64(totalTiming.CommitPubRandListTime.Nanoseconds())/float64(b.N), "ns/CommitPubRandList")
		})
	}
}

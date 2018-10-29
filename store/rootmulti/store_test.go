package rootmulti

import (
	"testing"

	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto/merkle"
	dbm "github.com/tendermint/tendermint/libs/db"

	"github.com/cosmos/cosmos-sdk/store/iavl"
	"github.com/cosmos/cosmos-sdk/store/types"
)

const useDebugDB = false

func TestStoreType(t *testing.T) {
	db := dbm.NewMemDB()
	store := NewStore(db)
	store.MountKVStoreWithDB(iavl.NewKey("store1"), db)
}

func TestStoreMount(t *testing.T) {
	db := dbm.NewMemDB()
	store := NewStore(db)

	key1 := iavl.NewKey("store1")
	key2 := iavl.NewKey("store2")
	dup1 := iavl.NewKey("store1")

	require.NotPanics(t, func() { store.MountKVStoreWithDB(key1, db) })
	require.NotPanics(t, func() { store.MountKVStoreWithDB(key2, db) })

	require.Panics(t, func() { store.MountKVStoreWithDB(key1, db) })
	require.Panics(t, func() { store.MountKVStoreWithDB(dup1, db) })
}

func TestMultistoreCommitLoad(t *testing.T) {
	var db dbm.DB = dbm.NewMemDB()
	if useDebugDB {
		db = dbm.NewDebugDB("CMS", db)
	}
	store := newMultiStoreWithMounts(db)
	err := store.LoadLatestVersion()
	require.Nil(t, err)

	// New store has empty last commit.
	commitID := types.CommitID{}
	checkStore(t, store, commitID, commitID)

	// Make sure we can get stores by name.
	s1 := store.getStoreByName("store1")
	require.NotNil(t, s1)
	s3 := store.getStoreByName("store3")
	require.NotNil(t, s3)
	s77 := store.getStoreByName("store77")
	require.Nil(t, s77)

	// Make a few commits and check them.
	nCommits := int64(3)
	for i := int64(0); i < nCommits; i++ {
		commitID = store.Commit()
		expectedCommitID := getExpectedCommitID(store, i+1)
		checkStore(t, store, expectedCommitID, commitID)
	}

	// Load the latest multistore again and check version.
	store = newMultiStoreWithMounts(db)
	err = store.LoadLatestVersion()
	require.Nil(t, err)
	commitID = getExpectedCommitID(store, nCommits)
	checkStore(t, store, commitID, commitID)

	// Commit and check version.
	commitID = store.Commit()
	expectedCommitID := getExpectedCommitID(store, nCommits+1)
	checkStore(t, store, expectedCommitID, commitID)

	// Load an older multistore and check version.
	ver := nCommits - 1
	store = newMultiStoreWithMounts(db)
	err = store.LoadMultiStoreVersion(ver)
	require.Nil(t, err)
	commitID = getExpectedCommitID(store, ver)
	checkStore(t, store, commitID, commitID)

	// XXX: commit this older version
	commitID = store.Commit()
	expectedCommitID = getExpectedCommitID(store, ver+1)
	checkStore(t, store, expectedCommitID, commitID)

	// XXX: confirm old commit is overwritten and we have rolled back
	// LatestVersion
	store = newMultiStoreWithMounts(db)
	err = store.LoadLatestVersion()
	require.Nil(t, err)
	commitID = getExpectedCommitID(store, ver+1)
	checkStore(t, store, commitID, commitID)
}

func TestParsePath(t *testing.T) {
	_, _, err := parsePath("foo")
	require.Error(t, err)

	store, subpath, err := parsePath("/foo")
	require.NoError(t, err)
	require.Equal(t, store, "foo")
	require.Equal(t, subpath, "")

	store, subpath, err = parsePath("/fizz/bang/baz")
	require.NoError(t, err)
	require.Equal(t, store, "fizz")
	require.Equal(t, subpath, "/bang/baz")

	substore, subsubpath, err := parsePath(subpath)
	require.NoError(t, err)
	require.Equal(t, substore, "bang")
	require.Equal(t, subsubpath, "/baz")

}

func TestMultiStoreQuery(t *testing.T) {
	db := dbm.NewMemDB()
	multi := newMultiStoreWithMounts(db)
	err := multi.LoadLatestVersion()
	require.Nil(t, err)

	k, v := []byte("wind"), []byte("blows")
	k2, v2 := []byte("water"), []byte("flows")
	// v3 := []byte("is cold")

	cid := multi.Commit()

	// Make sure we can get by name.
	garbage := multi.getStoreByName("bad-name")
	require.Nil(t, garbage)

	// Set and commit data in one store.
	store1 := multi.getStoreByName("store1").(types.KVStore)
	store1.Set(k, v)

	// ... and another.
	store2 := multi.getStoreByName("store2").(types.KVStore)
	store2.Set(k2, v2)

	// Commit the multistore.
	cid = multi.Commit()
	ver := cid.Version

	// Reload multistore from database
	multi = newMultiStoreWithMounts(db)
	err = multi.LoadLatestVersion()
	require.Nil(t, err)

	// Test bad path.
	query := abci.RequestQuery{Path: "/key", Data: k, Height: ver}
	qres := multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeUnknownRequest), types.ABCICodeType(qres.Code))

	query.Path = "h897fy32890rf63296r92"
	qres = multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeUnknownRequest), types.ABCICodeType(qres.Code))

	// Test invalid store name.
	query.Path = "/garbage/key"
	qres = multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeUnknownRequest), types.ABCICodeType(qres.Code))

	// Test valid query with data.
	query.Path = "/store1/key"
	qres = multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeOK), types.ABCICodeType(qres.Code))
	require.Equal(t, v, qres.Value)

	// Test valid but empty query.
	query.Path = "/store2/key"
	query.Prove = true
	qres = multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeOK), types.ABCICodeType(qres.Code))
	require.Nil(t, qres.Value)

	// Test store2 data.
	query.Data = k2
	qres = multi.Query(query)
	require.Equal(t, types.ToABCICode(types.CodeOK), types.ABCICodeType(qres.Code))
	require.Equal(t, v2, qres.Value)
}

//-----------------------------------------------------------------------
// utils

func newMultiStoreWithMounts(db dbm.DB) *Store {
	store := NewStore(db)
	store.MountKVStoreWithDB(iavl.NewKey("store1"), nil)
	store.MountKVStoreWithDB(iavl.NewKey("store2"), nil)
	store.MountKVStoreWithDB(iavl.NewKey("store3"), nil)
	return store
}

func checkStore(t *testing.T, store *Store, expect, got types.CommitID) {
	require.Equal(t, expect, got)
	require.Equal(t, expect, store.LastCommitID())

}

func getExpectedCommitID(store *Store, ver int64) types.CommitID {
	return types.CommitID{
		Version: ver,
		Hash:    hashStores(store.kvstores),
	}
}

func hashStores( /*TODO: multistores*/ kvstores map[types.KVStoreKey]types.CommitKVStore) []byte {
	m := make(map[string]merkle.Hasher, len(kvstores))
	for key, store := range kvstores {
		name := key.Name()
		m[name] = storeInfo{
			Name: name,
			Core: storeCore{
				CommitID: store.LastCommitID(),
				// StoreType: store.GetStoreType(),
			},
		}
	}
	return merkle.SimpleHashFromMap(m)
}

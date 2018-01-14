package main

import (
	"os"
	"fmt"
	"path"
	"net/http"
	"io/ioutil"
	"github.com/gorilla/mux"
	"github.com/jimlawless/whereami"
	"github.com/tendermint/tmlibs/log"
	"github.com/tendermint/tmlibs/common"
	"github.com/tendermint/abci/types"

	sdk "github.com/cosmos/cosmos-sdk"
	"github.com/cosmos/cosmos-sdk/stack"
	"github.com/cosmos/cosmos-sdk/app"
	"github.com/cosmos/cosmos-sdk/genesis"
	"github.com/cosmos/cosmos-sdk/modules/nonce"
	"github.com/cosmos/cosmos-sdk/modules/base"
	"github.com/cosmos/cosmos-sdk/modules/auth"
	"github.com/cosmos/cosmos-sdk/modules/ibc"
	"github.com/cosmos/cosmos-sdk/modules/roles"
	"github.com/cosmos/cosmos-sdk/modules/fee"
	"github.com/cosmos/cosmos-sdk/modules/coin"
	"github.com/golang/protobuf/proto"
)

const EyesCacheSize = 10000
var handler sdk.Handler
var store *app.StoreApp
var chainID string
var logger = log.NewTMLogger(log.NewSyncWriter(os.Stdout))

func main() {
	rootDir := os.ExpandEnv("$HOME/.cybermiles-travis")
	chainID = "travis-1"

	// configure handlers the type is sdk.Handler
	handler = stack.New(
		base.Logger{},
		stack.Recovery{},
		auth.Signatures{},
		base.Chain{},
		stack.Checkpoint{OnCheck: true},
		nonce.ReplayCheck{},
	).
		IBC(ibc.NewMiddleware()).
		Apps(
			roles.NewMiddleware(),
			fee.NewSimpleFeeMiddleware(coin.Coin{"strings", 0}, fee.Bank),
			stack.Checkpoint{OnDeliver: true},
		).
		Dispatch(
			coin.NewHandler(),
			stack.WrapHandler(roles.NewHandler()),
			stack.WrapHandler(ibc.NewHandler()),
			// stake.NewHandler(),
		)

	// The blockchain's offchain data store managed by the ABCI app. https://github.com/cosmos/cosmos-sdk/blob/master/app/store.go
	// The store object contains:
	//   The block height
	//   A state object of the state.State type, which is persisted as a DB on the disk (accessible from Java). It also contains the working checktx and delivertx state trees (SimpleDB). https://github.com/cosmos/cosmos-sdk/blob/master/state/merkle.go
	// Here we create a new blank store. Then the ABCI starts to process the tx synced from the blockchain, and will build up and state and height over time.
	var err error
	store, err = app.NewStoreApp(
		chainID,
		path.Join(rootDir, "data", "merkleeyes.db"),
		EyesCacheSize,
		logger,
	)
	if err != nil {
		return
	}

	// genesis.Load(handler, rootDir + "/genesis.json")
	opts, err := genesis.GetOptions(rootDir + "/genesis.json")
	if err != nil {
		return
	}
	// execute all the genesis init options
	// abort on any error
	for _, opt := range opts {
		if opt.Module == "base" {continue}
		state := store.Append()
		var res string
		fmt.Printf("%s...\nInit State: %s %s %s\n\n", whereami.WhereAmI(), opt.Module, opt.Key, opt.Value)
		res, err = handler.InitState(logger, state, opt.Module, opt.Key, opt.Value)
		fmt.Printf("res = %v\n", res)
		fmt.Printf("state = %v\n", state)
		if err != nil {
			fmt.Printf("%s...\nInit State ERROR: %s\n\n", whereami.WhereAmI(), res)
			logger.With("initState Error", res)
			return
		}
	}
	store.Commit()

	// start the REST server
	r := mux.NewRouter()
	r.HandleFunc("/check_tx", checkTx)
	r.HandleFunc("/deliver_tx", deliverTx)
	r.HandleFunc("/query", query)
	// http.Handle("/", r)
	http.ListenAndServe(":8088", r)

}


func checkTx(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	tx, err := sdk.LoadTx(b)
	if err != nil {
		common.WriteError(w, err)
		return
	}
	txJSON, _ := tx.MarshalJSON()
	fmt.Printf("%s...\ntxJSON: \t%s\n\n", whereami.WhereAmI(), string(txJSON))

	ctx := stack.NewContext(
		chainID,
		store.WorkingHeight(),
		logger.With("call", "checktx"),
	)
	// The store.Check() method gets a SimpleDB containing the working memory of the checktx from the DB
	res, err := handler.CheckTx(ctx, store.Check(), tx)
	fmt.Printf("%s...\nresult: \t%#v\n\n", whereami.WhereAmI(), res)

	// common.WriteSuccess(w, res)
	// returns the tx so that the Java app can continue working on it
	common.WriteSuccess(w, txJSON)
}

func deliverTx(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	tx, err := sdk.LoadTx(b)
	if err != nil {
		common.WriteError(w, err)
		return
	}
	txJSON, _ := tx.MarshalJSON()
	fmt.Printf("%s...\ntxJSON: \t%s\n\n", whereami.WhereAmI(), string(txJSON))

	ctx := stack.NewContext(
		chainID,
		store.WorkingHeight(),
		logger.With("call", "delivertx"),
	)
	// The store.Append() method gets a SimpleDB containing the working memory of the delivertx from the DB
	res, err := handler.DeliverTx(ctx, store.Append(), tx)
	fmt.Printf("%s...\nresult: \t%#v\n\n", whereami.WhereAmI(), res)

	// common.WriteSuccess(w, res)
	// returns the tx so that the Java app can continue working on it
	common.WriteSuccess(w, txJSON)
}

func query(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	req := &types.RequestQuery{}
	if err := proto.Unmarshal(b, req); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fmt.Printf("RequestQuery Path: %v\n", req.Path)
	fmt.Printf("RequestQuery Height: %v\n", req.Height)
	fmt.Printf("RequestQuery Prove: %v\n", req.Prove)
	fmt.Printf("RequestQuery Data: %v\n", req.Data)

	resp := store.Query(*req)

	out, err := proto.Marshal(&resp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fmt.Printf("Response Code: %v\n", resp.Code)
	fmt.Printf("Response Height: %v\n", resp.Height)
	fmt.Printf("Response Index: %v\n", resp.Index)
	fmt.Printf("Response Key: %v\n", resp.Key)
	fmt.Printf("Response Proof: %v\n", resp.Proof)
	fmt.Printf("Response Value: %v\n", resp.Value)

	common.WriteSuccess(w, out)
}

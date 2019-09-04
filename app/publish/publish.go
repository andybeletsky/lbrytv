package publish

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/lbryio/lbrytv/app/proxy"
	"github.com/lbryio/lbrytv/app/users"
	"github.com/lbryio/lbrytv/internal/lbrynet"

	ljsonrpc "github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/ybbus/jsonrpc"
)

const uploadPath = "/tmp"

const fileField = "file"
const jsonrpcPayloadField = "json_payload"

// Publisher is responsible for sending data to lbrynet
// and should take file path, account ID and client query as a slice of bytes.
type Publisher interface {
	Publish(string, string, []byte) ([]byte, error)
}

// LbrynetPublisher is an implementation of SDK publisher.
type LbrynetPublisher struct{}

// UploadHandler glues HTTP uploads to the Publisher
type UploadHandler struct {
	Publisher  Publisher
	uploadPath string
}

// NewUploadHandler returns a HTTP upload handler object.
func NewUploadHandler(uploadPath string, publisher Publisher) UploadHandler {
	return UploadHandler{
		Publisher:  publisher,
		uploadPath: uploadPath,
	}
}

// Publish takes a file path, account ID and client JSON-RPC query,
// patches the query and sends it to the SDK for processing.
// Resulting response is then returned back as a slice of bytes.
func (p *LbrynetPublisher) Publish(filePath, accountID string, rawQuery []byte) ([]byte, error) {
	// var rpcParams *lbrynet.PublishParams
	// var rpcParams *ljsonrpc.StreamCreateOptions
	rpcParams := struct {
		Name                          string  `json:"name"`
		Bid                           string  `json:"bid"`
		FilePath                      string  `json:"file_path,omitempty"`
		FileSize                      *string `json:"file_size,omitempty"`
		IncludeProtoBuf               bool    `json:"include_protobuf"`
		Blocking                      bool    `json:"blocking"`
		*ljsonrpc.StreamCreateOptions `json:",flatten"`
	}{}

	query, err := proxy.NewQuery(rawQuery)
	if err != nil {
		panic(err)
	}

	if err := query.ParamsToStruct(&rpcParams); err != nil {
		panic(err)
	}

	if rpcParams.FilePath != "__POST_FILE__" {
		panic("unknown file_path content")
	}

	bid, err := strconv.ParseFloat(rpcParams.Bid, 64)
	rpcParams.FilePath = filePath
	rpcParams.AccountID = &accountID

	result, err := lbrynet.Client.StreamCreate(rpcParams.Name, filePath, bid, *rpcParams.StreamCreateOptions)
	if err != nil {
		return nil, err
	}

	rpcResponse := jsonrpc.RPCResponse{Result: result}
	serialized, err := json.MarshalIndent(rpcResponse, "", "  ")
	if err != nil {
		return nil, err
	}
	return serialized, nil
}

// Handle is where HTTP upload is handled and passed on to Publisher.
// It should be wrapped with users.Authenticator.Wrap before it can be used
// in a mux.Router.
func (h UploadHandler) Handle(w http.ResponseWriter, r *users.AuthenticatedRequest) {
	if !r.IsAuthenticated() {
		var authErr Error
		if r.AuthFailed() {
			authErr = NewAuthError(r.AuthError)
		} else {
			authErr = ErrUnauthorized
		}
		w.WriteHeader(http.StatusOK)
		w.Write(authErr.AsBytes())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	f, err := h.CreateFile(r.AccountID, header.Filename)
	if err != nil {
		panic(err)
	}

	if num, err := io.Copy(f, file); err != nil {
		panic(err)
	} else {
		fmt.Println(num)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}

	response, err := h.Publisher.Publish(f.Name(), r.AccountID, []byte(r.FormValue(jsonrpcPayloadField)))
	if err != nil {
		panic(err)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(response)
}

// CreateFile opens an empty file for writing inside the account's designated folder.
// The final file path looks like `/upload_path/{account_id}/{random}_filename.ext`,
// where `account_id` is local SDK account ID and `random` is a random string generated by ioutil.
func (h UploadHandler) CreateFile(accountID string, origFilename string) (*os.File, error) {
	path, err := h.preparePath(accountID)
	if err != nil {
		panic(err)
	}
	return ioutil.TempFile(path, fmt.Sprintf("*_%v", origFilename))
}

func (h UploadHandler) preparePath(accountID string) (string, error) {
	path := path.Join(h.uploadPath, accountID)
	err := os.MkdirAll(path, os.ModePerm)
	return path, err
}

// Package server implement a file upload server.
package server

import (
	"io"
	"sync"

	"github.com/pkg/errors"

	pb "go.viam.com/utils/proto/rpc/examples/fileupload/v1"
)

// Server implements a simple file upload service.
type Server struct {
	mu sync.Mutex
	pb.UnimplementedFileUploadServiceServer
	fail bool
}

// SetFail instructs the server to fail at certain points in its execution.
func (srv *Server) SetFail(fail bool) {
	srv.mu.Lock()
	srv.fail = fail
	srv.mu.Unlock()
}

// UploadFile receives a file over a series of chunks.
func (srv *Server) UploadFile(server pb.FileUploadService_UploadFileServer) error {
	var haveName bool
	var fileName string
	var data []byte
	for {
		select {
		case <-server.Context().Done():
			return server.Context().Err()
		default:
		}
		req, err := server.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// at this point, you can do something with the file
				return server.SendAndClose(&pb.UploadFileResponse{Name: fileName, Size: int64(len(data))})
			}
			return err
		}
		switch d := req.Data.(type) {
		case (*pb.UploadFileRequest_Name):
			if haveName {
				return errors.New("received name more than once")
			}
			haveName = true
			fileName = d.Name
		case (*pb.UploadFileRequest_ChunkData):
			if !haveName {
				return errors.New("first provide file name")
			}
			data = append(data, d.ChunkData...)
		default:
			return errors.Errorf("unknown data type %T", req.Data)
		}
	}
}

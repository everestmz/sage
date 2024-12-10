package server

import (
	"context"
	"log"
	"net"
	"os"

	"github.com/everestmz/sage/docstate"
	pb "github.com/everestmz/sage/rpc/languageserver"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type StateServer struct {
	pb.UnimplementedLanguageServerStateServer
	ds *docstate.DocumentState
}

func NewStateServer(ds *docstate.DocumentState) *StateServer {
	return &StateServer{ds: ds}
}

func (s *StateServer) GetOpenDocuments(ctx context.Context, req *pb.GetOpenDocumentsRequest) (*pb.GetOpenDocumentsResponse, error) {
	response := &pb.GetOpenDocumentsResponse{
		Documents: make(map[string]*pb.TextDocument),
	}

	for uri, doc := range s.ds.OpenDocuments() {
		response.Documents[string(uri)] = &pb.TextDocument{
			Uri:            string(uri),
			LanguageId:     string(doc.LanguageID),
			Version:        int32(doc.Version),
			Text:           doc.Text,
			LastEdit:       timestamppb.New(doc.LastEdit),
			LastEditedLine: int32(doc.LastEditedLine),
		}
	}

	return response, nil
}

// StartStateServer starts the gRPC server on a Unix domain socket
func StartStateServer(ds *docstate.DocumentState, socketPath string) error {
	// Clean up existing socket if it exists
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}

	server := grpc.NewServer()
	pb.RegisterLanguageServerStateServer(server, NewStateServer(ds))

	go func() {
		if err := server.Serve(listener); err != nil {
			log.Printf("Failed to serve: %v", err)
		}
	}()

	return nil
}

package client

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "github.com/everestmz/sage/rpc/languageserver"
)

// Client represents a client to access the language server state
type Client struct {
	conn   *grpc.ClientConn
	client pb.LanguageServerStateClient
}

// NewClient creates a new client connected to the language server state
func NewClient(socketPath string) (*Client, error) {
	conn, err := grpc.Dial(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:   conn,
		client: pb.NewLanguageServerStateClient(conn),
	}, nil
}

// GetOpenDocuments retrieves all currently open documents from the language server
func (c *Client) GetOpenDocuments(ctx context.Context) (map[string]*pb.TextDocument, error) {
	response, err := c.client.GetOpenDocuments(ctx, &pb.GetOpenDocumentsRequest{})
	if err != nil {
		return nil, err
	}
	return response.Documents, nil
}

// Close closes the client connection
func (c *Client) Close() error {
	return c.conn.Close()
}

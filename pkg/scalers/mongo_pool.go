package scalers

import (
	"context"
	"fmt"
	"sync"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// mongoClientPool manages multiple MongoDB clients, supporting reuse of clients for the same URI.
type mongoClientPool struct {
	mu      sync.Mutex
	clients map[string]*mongoClientEntry // Mapping from URI to clientEntry
}

// mongoClientEntry contains a MongoDB client and a reference count
type mongoClientEntry struct {
	client   *mongo.Client
	refCount int
}

// pooledMongoClient wraps *mongo.Client and notifies ClientPool upon closing
type pooledMongoClient struct {
	*mongo.Client
	pool *mongoClientPool
	uri  string
}

// newMongoClientPool creates a new instance of ClientPool
func newMongoClientPool() *mongoClientPool {
	return &mongoClientPool{
		clients: make(map[string]*mongoClientEntry),
	}
}

var globalMongoClientPool = newMongoClientPool()

// GetClient retrieves a MongoDB client for the specified URI.
// If the client doesn't exist, it creates a new one.
// The returned PooledClient should call Close() when done.
func (cp *mongoClientPool) GetClient(ctx context.Context, uri string) (*pooledMongoClient, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	ce, ok := cp.clients[uri]
	if !ok {
		// Create a new client
		clientOpts := options.Client().ApplyURI(uri)
		client, err := mongo.Connect(ctx, clientOpts)
		if err != nil {
			return nil, err
		}

		err = client.Ping(ctx, readpref.Primary())
		if err != nil {
			return nil, fmt.Errorf("failed to ping mongodb: %w", err)
		}

		ce = &mongoClientEntry{
			client:   client,
			refCount: 1,
		}
		cp.clients[uri] = ce
	} else {
		// Client already exists, increment reference count
		ce.refCount++
	}

	// Return the wrapped PooledClient
	pc := &pooledMongoClient{
		Client: ce.client,
		pool:   cp,
		uri:    uri,
	}

	return pc, nil
}

// Close should be called when the PooledClient is no longer needed.
// It notifies the ClientPool to release the client.
func (pc *pooledMongoClient) Close(ctx context.Context) error {
	return pc.pool.releaseClient(pc.uri, ctx)
}

// releaseClient decrements the reference count of the client for the given URI.
// If the reference count reaches zero, it disconnects the client.
func (cp *mongoClientPool) releaseClient(uri string, ctx context.Context) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	ce, ok := cp.clients[uri]
	if !ok {
		return nil // Or return an error if preferred
	}

	ce.refCount--
	if ce.refCount == 0 {
		// Last reference, disconnect the client and remove it from the map
		err := ce.client.Disconnect(ctx)
		delete(cp.clients, uri)
		return err
	}
	return nil
}

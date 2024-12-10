package docstate

import (
	"sync"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type OpenDocument struct {
	protocol.TextDocumentItem

	LastEdit       time.Time
	LastEditedLine int
}

func NewDocumentState() *DocumentState {
	return &DocumentState{
		openDocuments: map[uri.URI]*OpenDocument{},
		docLock:       sync.Mutex{},
	}
}

type DocumentState struct {
	openDocuments map[uri.URI]*OpenDocument
	docLock       sync.Mutex
}

func (ds *DocumentState) OpenDocuments() map[uri.URI]OpenDocument {
	ds.docLock.Lock()
	defer ds.docLock.Unlock()

	docs := map[uri.URI]OpenDocument{}
	for uri, doc := range ds.openDocuments {
		docs[uri] = *doc
	}

	return docs
}

func (ds *DocumentState) GetOpenDocument(uri uri.URI) (OpenDocument, bool) {
	ds.docLock.Lock()
	defer ds.docLock.Unlock()

	doc, ok := ds.openDocuments[uri]
	if ok {
		return *doc, true
	}

	return OpenDocument{}, false
}

func (ds *DocumentState) OpenDocument(doc *protocol.TextDocumentItem) {
	ds.docLock.Lock()
	defer ds.docLock.Unlock()

	ds.openDocuments[doc.URI] = &OpenDocument{
		TextDocumentItem: *doc,

		LastEdit:       time.Now(),
		LastEditedLine: 0,
	}
}

func (ds *DocumentState) CloseDocument(uri uri.URI) {
	ds.docLock.Lock()
	defer ds.docLock.Unlock()

	delete(ds.openDocuments, uri)
}

func (ds *DocumentState) EditDocument(uri uri.URI, editFunc func(doc *protocol.TextDocumentItem) error) error {
	ds.docLock.Lock()
	defer ds.docLock.Unlock()

	openDoc := ds.openDocuments[uri]
	protocolDoc := openDoc.TextDocumentItem
	err := editFunc(&protocolDoc)
	if err != nil {
		return err
	}

	openDoc.TextDocumentItem = protocolDoc

	return nil
}

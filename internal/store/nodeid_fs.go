package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/c12s/star/internal/domain"
)

const clusterIdFileName = "clusterid"

type nodeIdFSStore struct {
	dirPath         string
	fileName        string
	filePath        string
	clusterFilePath string
}

func NewNodeIdFSStore(dirPath, fileName string) (domain.NodeIdStore, error) {
	err := os.MkdirAll(dirPath, 0755)
	if err != nil {
		return nil, err
	}
	return &nodeIdFSStore{
		dirPath:         dirPath,
		fileName:        fileName,
		filePath:        dirPath + string(filepath.Separator) + fileName,
		clusterFilePath: dirPath + string(filepath.Separator) + clusterIdFileName,
	}, nil
}

func (n nodeIdFSStore) Get() (*domain.NodeId, error) {
	fileContents, err := os.ReadFile(n.filePath)
	if err != nil {
		return nil, err
	}
	return &domain.NodeId{
		Value: strings.TrimSpace(string(fileContents)),
	}, nil
}

func (n nodeIdFSStore) Put(nodeId domain.NodeId) error {
	return os.WriteFile(n.filePath, []byte(nodeId.Value), 0666)
}

func (n nodeIdFSStore) Delete() error {
	return os.Remove(n.filePath)
}

func (n nodeIdFSStore) PutClusterId(clusterId string) error {
	return os.WriteFile(n.clusterFilePath, []byte(clusterId), 0666)
}

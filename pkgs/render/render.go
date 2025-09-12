package render

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/apply"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ApplyTemplate(reader io.Reader, vars map[string]string) (io.Reader, error) {
	contents, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	t, err := template.New("template").Option("missingkey=error").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("Failed to parse yaml through template: %v", err)
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, vars)
	if err != nil {
		return nil, fmt.Errorf("Failed to Execute template on buffer: %v", err)
	}
	return bytes.NewReader(buf.Bytes()), nil
}

func BinDataYamlFiles(dirPath string, binData embed.FS) ([]string, error) {
	var yamlFileDescriptors []string

	dir, err := binData.ReadDir(filepath.Join("bindata", dirPath))
	if err != nil {
		return nil, err
	}

	for _, f := range dir {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".yaml") {
			yamlFileDescriptors = append(yamlFileDescriptors, filepath.Join(dirPath, f.Name()))
		}
	}

	sort.Strings(yamlFileDescriptors)
	return yamlFileDescriptors, nil
}

func applyObjectFromBinData(logger logr.Logger, filePath string, data map[string]string, binData embed.FS, client client.Client, owner client.Object) error {
	file, err := binData.Open(filepath.Join("bindata", filePath))
	if err != nil {
		return fmt.Errorf("Failed to read file '%s': %v", filePath, err)
	}
	applied, err := ApplyTemplate(file, data)
	if err != nil {
		return fmt.Errorf("Failed to apply template on '%s': %v", filePath, err)
	}
	var obj *unstructured.Unstructured
	err = yaml.NewYAMLOrJSONDecoder(applied, 1024).Decode(&obj)
	if err != nil {
		return err
	}
	if owner != nil {
		if err := ctrl.SetControllerReference(owner, obj, client.Scheme()); err != nil {
			return err
		}
	}
	logger.Info("Preparing CR", "kind", obj.GetKind())
	if err := apply.ApplyObject(context.TODO(), client, obj); err != nil {
		// When resources (for example the VSP) is deployed multiple times in the case of 1 cluster,
		// we want to ignore already exists errors. Also handle conflict errors when resources are
		// created concurrently by multiple daemons (e.g. errors which occur when the resource has been modified since last read)
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Resource already exists, skipping creation", "kind", obj.GetKind(), "name", obj.GetName())
			return nil
		}

		if apierrors.IsConflict(err) {
			logger.Info("Resource conflict detected, skipping update", "kind", obj.GetKind(), "name", obj.GetName())
			return nil
		}
		return fmt.Errorf("failed to apply object %v with err: %v", obj, err)
	}
	return nil
}

func ApplyAllFromBinData(logger logr.Logger, binDataPath string, data map[string]string, binData embed.FS, client client.Client, owner client.Object) error {
	filePaths, err := BinDataYamlFiles(binDataPath, binData)
	if err != nil {
		return err
	}
	for _, f := range filePaths {
		err = applyObjectFromBinData(logger, f, data, binData, client, owner)
		if err != nil {
			return err
		}
	}
	return nil
}

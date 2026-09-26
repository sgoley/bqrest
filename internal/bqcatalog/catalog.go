package bqcatalog

import (
	"context"
	"fmt"

	"bqrest/internal/config"
	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type Resource struct {
	Table   string
	Type    string
	Columns []Column
}

type Column struct {
	Name string
	Type string
}

func List(ctx context.Context, connection config.Connection, datasetID, credentialsFile string) ([]Resource, error) {
	if !configuredDataset(connection, datasetID) {
		return nil, fmt.Errorf("dataset %q is not configured for connection %q", datasetID, connection.Name)
	}
	client, err := bigquery.NewClient(ctx, connection.ProjectID, option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("create BigQuery client: %w", err)
	}
	defer client.Close()

	iter := client.DatasetInProject(connection.ProjectID, datasetID).Tables(ctx)
	var resources []Resource
	for {
		table, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list dataset tables: %w", err)
		}
		metadata, err := table.Metadata(ctx, bigquery.WithMetadataView(bigquery.BasicMetadataView))
		if err != nil {
			return nil, fmt.Errorf("read metadata for %s: %w", table.TableID, err)
		}
		if metadata.Type != bigquery.RegularTable && metadata.Type != bigquery.ViewTable && metadata.Type != bigquery.MaterializedView {
			continue
		}
		resource := Resource{Table: table.TableID, Type: string(metadata.Type)}
		for _, field := range metadata.Schema {
			resource.Columns = append(resource.Columns, Column{Name: field.Name, Type: string(field.Type)})
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func configuredDataset(connection config.Connection, dataset string) bool {
	for _, configured := range connection.Datasets {
		if configured == dataset {
			return true
		}
	}
	return false
}

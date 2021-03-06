package main

import (
	"log"
	"os"
	"os/signal"
	"time"

	"log-aggregator/cursor"
	"log-aggregator/destinations"
	"log-aggregator/destinations/firehose"
	"log-aggregator/destinations/stdout"
	"log-aggregator/pipeline"
	"log-aggregator/sources"
	sjournal "log-aggregator/sources/journal"
	"log-aggregator/sources/mock"
	"log-aggregator/transform"
	"log-aggregator/transform/aws"
	"log-aggregator/transform/journal"
	"log-aggregator/transform/json"
	"log-aggregator/transform/eleven"
	"log-aggregator/transform/k8"
)

const (
	EnvK8ConfigPath                = "FAIR_LOG_K8_CONFIG_PATH"
	EnvK8Regex                     = "FAIR_LOG_K8_CONTAINER_NAME_REGEX"
	EnvCursorPath                  = "FAIR_LOG_CURSOR_PATH"
	EnvMockSource                  = "FAIR_LOG_MOCK_SOURCE"
	EnvMockDestination             = "FAIR_LOG_MOCK_DESTINATION"
	EnvFirehoseStream              = "FAIR_LOG_FIREHOSE_STREAM"
	EnvFirehoseCredentialsEndpoint = "FAIR_LOG_FIREHOSE_CREDENTIALS_ENDPOINT"
	EnvK8NodeName                  = "EC2_METADATA_LOCAL_HOSTNAME"
)

func main() {
	var err error
	var source sources.Source
	var destination destinations.Destination
	var logCursor cursor.DB
	var transformers []transform.Transformer

	// Setup cursor
	if cursorPath := os.Getenv(EnvCursorPath); cursorPath == "" {
		log.Fatalf("%s must be set", EnvCursorPath)
	} else {
		logCursor, err = cursor.New(cursorPath)
		if err != nil {
			panic(err)
		}
	}

	// Setup source
	if os.Getenv(EnvMockSource) == "true" {
		source = mock.New(time.Second * 2)
	} else {
		source, err = sjournal.New(sjournal.ClientConfig{
			Cursor: logCursor.Cursor(),
		})
		if err != nil {
			panic(err)
		}
	}

	// Setup destination
	if os.Getenv(EnvMockDestination) == "true" {
		destination = stdout.New()
	} else {
		streamName := os.Getenv(EnvFirehoseStream)
		if streamName == "" {
			log.Fatalf("%s must be set", EnvFirehoseStream)
		}
		destination = firehose.New(firehose.Config{
			EC2MetadataEndpoint: os.Getenv(EnvFirehoseCredentialsEndpoint),
			FirehoseStream:      streamName,
		})
	}

	// Setup transformer pipeline
	transformers = []transform.Transformer{
		journal.Transform,
		json.Transform,
		aws.New(),
		eleven.New(),
	}

	if configPath := os.Getenv(EnvK8ConfigPath); configPath != "" {
		k8Transformer := k8.New(k8.Config{
			K8ConfigPath:                  configPath,
			NodeName:                      os.Getenv(EnvK8NodeName),
			MaxPodsCache:                  100,
			KubernetesContainerNameRegexp: os.Getenv(EnvK8Regex),
		})
		transformers = append(transformers, k8Transformer.Transform)
	}

	logPipeline, err := pipeline.New(pipeline.Config{
		MaxBuffer:    200,
		Cursor:       logCursor,
		Input:        source,
		Destination:  destination,
		Transformers: transformers,
	})
	if err != nil {
		panic(err)
	}
	logPipeline.Start()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	<-signals
	logPipeline.Stop(time.Second * 30)
}

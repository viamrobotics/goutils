// Package machine_learning contains utilities for machine learning.
package machine_learning

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/pkg/errors"
	datav1 "go.viam.com/api/app/data/v1"
	mlv1 "go.viam.com/api/app/mltraining/v1"
)

// SingleLabelClassification defines the format of the data in jsonlines for single label classification.
type SingleLabelClassification struct {
	ImageGCSURI              string     `json:"image_gcs_uri"`
	ClassificationAnnotation Annotation `json:"classification_annotation"`
}

// MultiLabelClassification defines the format of the data in jsonlines for multi label classification.
type MultiLabelClassification struct {
	ImageGCSURI               string       `json:"image_gcs_uri"`
	ClassificationAnnotations []Annotation `json:"classification_annotations"`
}

// Annotation holds the label associated with the image.
type Annotation struct {
	AnnotationLabel string `json:"annotation_label"`
}

// ObjectDetection defines the format of the data in jsonlines for object detection.
type ObjectDetection struct {
	ImageGCSURI     string           `json:"image_gcs_uri"`
	BBoxAnnotations []BBoxAnnotation `json:"bounding_box_annotations"`
}

// CustomTrainingMetadata defines the format of the data in jsonlines for custom training.
type CustomTrainingMetadata struct {
	ImagePath                 string           `json:"image_path"`
	ClassificationAnnotations []Annotation     `json:"classification_annotations"`
	BBoxAnnotations           []BBoxAnnotation `json:"bounding_box_annotations"`
	BinaryDataID              string           `json:"binary_data_id,omitempty"`
	Timestamp                 string           `json:"timestamp"`
	RobotID                   string           `json:"robot_id,omitempty"`
	PartID                    string           `json:"part_id"`
	ComponentName             string           `json:"component_name"`
	OrganizationID            string           `json:"organization_id,omitempty"`
	LocationID                string           `json:"location_id,omitempty"`
}

// BBoxAnnotation holds the information associated with each bounding box.
type BBoxAnnotation struct {
	ID              string  `json:"id"`
	AnnotationLabel string  `json:"annotation_label"`
	XMinNormalized  float64 `json:"x_min_normalized"`
	XMaxNormalized  float64 `json:"x_max_normalized"`
	YMinNormalized  float64 `json:"y_min_normalized"`
	YMaxNormalized  float64 `json:"y_max_normalized"`
}

// ImageMetadata defines the metadata for an image.
type ImageMetadata struct {
	Timestamp      time.Time
	Tags           []string
	Annotations    *datav1.Annotations
	Bucket         string
	Path           string
	BinaryDataID   string
	OrganizationID string
	LocationID     string
	RobotID        string
	PartID         string
	ComponentName  string
}

// CloseableWriter defines an interface encompassing io.Writer and io.Closer.
type CloseableWriter interface {
	io.Writer
	io.Closer
}

var (
	// ErrJSONFormatting is the error returned when formatting JSON fails.
	ErrJSONFormatting = errors.New("error formatting JSON")
	// ErrFileWriting is the error returned when writing to a file fails.
	ErrFileWriting = errors.New("error writing to file")
)

// UnknownLabel is the label used for unlabeled data.
const UnknownLabel = "VIAM_UNKNOWN"

// ImageMetadataToJSONLines takes a user-defined filter, labels, and model type and produces a JSONLines for model training.
// If no requested tags are provided, all annotations for the data are returned.
func ImageMetadataToJSONLines(matchingData []*ImageMetadata,
	requestedTags []string, requestedModelType mlv1.ModelType, wc CloseableWriter,
) (map[string]int32, int, error) {
	if len(matchingData) == 0 {
		return nil, 0, errors.New("no matching datum to transform")
	}

	var tooManyLabels int
	labelsCount := make(map[string]int32)
	// Make JSONLines file
	for _, datum := range matchingData {
		// Join together bucket and blob path manually, since blob path may have extra slashes.
		blobPath := strings.Join([]string{datum.Bucket, datum.Path}, "/")
		var jsonl any

		allLabelsSet := make(map[string]struct{})
		for _, tag := range datum.Tags {
			allLabelsSet[tag] = struct{}{}
		}
		for _, annotation := range datum.Annotations.GetClassifications() {
			allLabelsSet[annotation.GetLabel()] = struct{}{}
		}
		// For custom training, there are no requested tags so all annotations should be returned.
		if requestedTags == nil {
			annotations := []Annotation{}
			for label := range allLabelsSet {
				annotations = append(annotations, Annotation{AnnotationLabel: label})
			}

			matchingAnnotations := getMatchingBBoxes(datum.Annotations.GetBboxes(), nil)
			if datum.Bucket == "" {
				// If bucket is empty, then this is for local use (RDK), otherwise for cloud training.
				blobPath = datum.Path
			} else {
				blobPath = strings.Join([]string{"/gcs", blobPath}, "/")
			}
			jsonl = CustomTrainingMetadata{
				ImagePath:                 blobPath,
				ClassificationAnnotations: annotations,
				BBoxAnnotations:           matchingAnnotations,
				Timestamp:                 datum.Timestamp.String(),
				BinaryDataID:              datum.BinaryDataID,
				RobotID:                   datum.RobotID,
				OrganizationID:            datum.OrganizationID,
				LocationID:                datum.LocationID,
				PartID:                    datum.PartID,
				ComponentName:             datum.ComponentName,
			}
		} else {
			var labels []string
			for label := range allLabelsSet {
				labels = append(labels, label)
			}
			switch requestedModelType {
			case mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION:
				var annotation Annotation
				matchingTags := getMatchingTags(labels, requestedTags)
				switch len(matchingTags) {
				case 0:
					annotation = Annotation{AnnotationLabel: UnknownLabel}
					labelsCount[UnknownLabel]++
				case 1:
					annotation = Annotation{AnnotationLabel: matchingTags[0]}
					labelsCount[annotation.AnnotationLabel]++
				default:
					// TODO(DATA-1542): Add logging for how many images were skipped and surface back to the user.
					tooManyLabels++
					continue
				}
				jsonl = SingleLabelClassification{
					ImageGCSURI:              blobPath,
					ClassificationAnnotation: annotation,
				}
			case mlv1.ModelType_MODEL_TYPE_MULTI_LABEL_CLASSIFICATION:
				annotations := []Annotation{}
				matchingTags := getMatchingTags(labels, requestedTags)
				if len(matchingTags) == 0 {
					annotations = append(annotations, Annotation{AnnotationLabel: UnknownLabel})
					labelsCount[UnknownLabel]++
				} else {
					for _, tag := range matchingTags {
						annotations = append(annotations, Annotation{AnnotationLabel: tag})
						labelsCount[tag]++
					}
				}
				jsonl = MultiLabelClassification{
					ImageGCSURI:               blobPath,
					ClassificationAnnotations: annotations,
				}
			case mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION:
				matchingAnnotations := getMatchingBBoxes(datum.Annotations.GetBboxes(), requestedTags)
				jsonl = ObjectDetection{
					ImageGCSURI:     blobPath,
					BBoxAnnotations: matchingAnnotations,
				}
				if len(matchingAnnotations) == 0 {
					labelsCount[UnknownLabel]++
				}
				for _, annotation := range matchingAnnotations {
					labelsCount[annotation.AnnotationLabel]++
				}

			case mlv1.ModelType_MODEL_TYPE_UNSPECIFIED:
				return nil, 0, errors.New("model type not specified")
			}
		}

		line, err := json.Marshal(jsonl)
		if err != nil {
			return nil, 0, errors.Wrap(ErrJSONFormatting, err.Error())
		}
		line = append(line, "\n"...)
		_, err = wc.Write(line)
		if err != nil {
			return nil, 0, errors.Wrap(ErrFileWriting, err.Error())
		}
	}

	if requestedModelType == mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION {
		return labelsCount, len(matchingData) - tooManyLabels, nil
	}
	return labelsCount, len(matchingData), nil
}

// getMatchingTags checks to see if any of the labels are in tags.
func getMatchingTags(tags, labels []string) []string {
	match := []string{}
	for _, reqLabel := range labels {
		for _, tag := range tags {
			if reqLabel == tag {
				match = append(match, reqLabel)
			}
		}
	}
	return match
}

// getMatchingBBoxes returns bounding box annotations that match the requested labels;
// if no labels are provided, returns all bounding box annotations.
func getMatchingBBoxes(annotations []*datav1.BoundingBox, labels []string) []BBoxAnnotation {
	match := []BBoxAnnotation{}
	for _, annotation := range annotations {
		bbox := BBoxAnnotation{
			ID:              annotation.GetId(),
			AnnotationLabel: annotation.GetLabel(),
			XMinNormalized:  annotation.GetXMinNormalized(),
			XMaxNormalized:  annotation.GetXMaxNormalized(),
			YMinNormalized:  annotation.GetYMinNormalized(),
			YMaxNormalized:  annotation.GetYMaxNormalized(),
		}
		if labels != nil {
			for _, reqLabel := range labels {
				if annotation.GetLabel() == reqLabel {
					match = append(match, bbox)
				}
			}
		} else {
			match = append(match, bbox)
		}
	}
	return match
}

package machine_learning

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	datav1 "go.viam.com/api/app/data/v1"
	mlv1 "go.viam.com/api/app/mltraining/v1"
	"go.viam.com/test"
	"go.viam.com/utils/artifact"
)

var (
	org1        = "matching_org1"
	loc1        = "loc1"
	jpegFileExt = ".jpeg"
	pngFileExt  = ".png"
	gifFileExt  = ".gif"
	zipExt      = ".gz"

	singleLabelDirName    = "test_fake_bucket/single"
	multiLabelDirName     = "test_fake_bucket/multi"
	objDetectionDirName   = "test_fake_bucket/detection"
	customTrainingDirName = "test_fake_bucket/custom"
	someBucket            = "some-bucket-name"

	singleClassificationLabel      = []string{"cat"}
	singleClassificationMultiLabel = []string{"cat", "dog", "turtle", "penguin"}
	multiClassificationLabels      = []string{
		"daisy", "dandelion", "roses", "sunflowers", "tulips",
		"medium_shot", "full_shot", "closeup", "extreme_closeup",
	}
	objectDetectionLabels = []string{
		"cat", "dog",
	}

	fakeData1 = &ImageMetadata{
		Tags:      []string{"cat"},
		Bucket:    singleLabelDirName,
		Path:      "filename1.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	fakeData2 = &ImageMetadata{
		Tags:      []string{"cat"},
		Bucket:    singleLabelDirName,
		Path:      "filename2.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	fakeData3 = &ImageMetadata{
		Tags:      []string{"dog"},
		Bucket:    singleLabelDirName,
		Path:      "filename3.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	fakeData4 = &ImageMetadata{
		Tags:      []string{"turtle"},
		Bucket:    singleLabelDirName,
		Path:      "filename4.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC),
	}
	fakeData5 = &ImageMetadata{
		Tags:      []string{"penguin"},
		Bucket:    singleLabelDirName,
		Path:      "filename5.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	}

	fakeMultiLabelData1 = &ImageMetadata{
		Tags:      []string{"daisy", "full_shot"},
		Bucket:    multiLabelDirName,
		Path:      "filename1.jpeg" + zipExt,
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	fakeMultiLabelData2 = &ImageMetadata{
		Tags:      []string{"dandelion", "medium_shot"},
		Bucket:    multiLabelDirName,
		Path:      "filename2" + gifFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	fakeMultiLabelData3 = &ImageMetadata{
		Tags:      []string{"roses", "extreme_closeup"},
		Bucket:    multiLabelDirName,
		Path:      "filename3" + pngFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	fakeMultiLabelData4 = &ImageMetadata{
		Tags:      []string{"sunflowers", "closeup"},
		Bucket:    multiLabelDirName,
		Path:      "filename4" + jpegFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC),
	}
	fakeMultiLabelData5 = &ImageMetadata{
		Tags:      []string{"tulips", "extreme_closeup"},
		Bucket:    multiLabelDirName,
		Path:      "filename5" + pngFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	}

	fakeObjDetectionData1 = &ImageMetadata{
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "2",
					Label:          "cat",
					XMinNormalized: 0.2,
					XMaxNormalized: 0.22,
					YMinNormalized: 0.3,
					YMaxNormalized: 0.33,
				},
				{
					Id:             "1",
					Label:          "dog",
					XMinNormalized: 0.1,
					XMaxNormalized: 0.11,
					YMinNormalized: 0.2,
					YMaxNormalized: 0.22,
				},
			},
		},
		Bucket:    objDetectionDirName,
		Path:      "filename1" + pngFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	fakeObjDetectionData2 = &ImageMetadata{
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "3",
					Label:          "cat",
					XMinNormalized: 0.4,
					XMaxNormalized: 0.44,
					YMinNormalized: 0.5,
					YMaxNormalized: 0.55,
				},
				{
					Id:             "4",
					Label:          "dog",
					XMinNormalized: 0.5,
					XMaxNormalized: 0.55,
					YMinNormalized: 0.6,
					YMaxNormalized: 0.66,
				},
			},
		},
		Bucket:    objDetectionDirName,
		Path:      "filename2" + jpegFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	fakeObjDetectionData3 = &ImageMetadata{
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "5",
					Label:          "cat",
					XMinNormalized: 0.4,
					XMaxNormalized: 0.44,
					YMinNormalized: 0.5,
					YMaxNormalized: 0.55,
				},
			},
		},
		Bucket:    objDetectionDirName,
		Path:      "filename3" + jpegFileExt + zipExt,
		Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
	}

	fakeCustomData4 = &ImageMetadata{
		Tags: []string{"cat"},
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "5",
					Label:          "cat",
					XMinNormalized: 0.2,
					XMaxNormalized: 0.22,
					YMinNormalized: 0.3,
					YMaxNormalized: 0.33,
				},
			},
		},
		Bucket:        customTrainingDirName,
		Path:          "filename4" + jpegFileExt + zipExt,
		PartID:        "part1",
		ComponentName: "component1",
		Timestamp:     time.Time{}, // Zero timestamp to match expected output
	}
	fakeCustomData5 = &ImageMetadata{
		Tags: []string{"cat", "dog"},
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "6",
					Label:          "cat",
					XMinNormalized: 0.4,
					XMaxNormalized: 0.44,
					YMinNormalized: 0.5,
					YMaxNormalized: 0.55,
				},
				{
					Id:             "7",
					Label:          "dog",
					XMinNormalized: 0.5,
					XMaxNormalized: 0.55,
					YMinNormalized: 0.6,
					YMaxNormalized: 0.66,
				},
			},
		},
		Bucket:        customTrainingDirName,
		Path:          "filename5" + jpegFileExt + zipExt,
		PartID:        "part1",
		ComponentName: "component2",
		Timestamp:     time.Time{}, // Zero timestamp to match expected output
	}
)

// mockWriter implements CloseableWriter for testing
type mockWriter struct {
	buf    *bytes.Buffer
	closed bool
}

func newMockWriter() *mockWriter {
	return &mockWriter{
		buf: &bytes.Buffer{},
	}
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *mockWriter) Close() error {
	m.closed = true
	return nil
}

func TestImageMetadataToJSONLines(t *testing.T) {
	tests := []struct {
		name                     string
		imageMetadata            []*ImageMetadata
		modelType                mlv1.ModelType
		labels                   []string
		minImagesObjectDetection int
		minBBoxesPerLabel        int
		minImagesPerLabel        int
		maxRatioUnlabeledImages  float64
		expJSONFile              string
		expectedErr              error
	}{
		{
			name: "Only one specified label for single label classification " +
				"results in file with one classification_annotation with string label and UNSPECIFIED others",
			imageMetadata:           []*ImageMetadata{fakeData1, fakeData2, fakeData3},
			modelType:               mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION,
			labels:                  singleClassificationLabel,
			minImagesPerLabel:       1, // Lower threshold for this test
			maxRatioUnlabeledImages: .4,
			expJSONFile:             filepath.Join(singleLabelDirName, "fakedata_single_label_binary.jsonl"),
		},
		{
			name: "Multiple specified labels for single label classification " +
				"results in file with only one classification_annotation per image",
			imageMetadata: []*ImageMetadata{
				fakeData1, fakeData2, fakeData3, fakeData4, fakeData5,
			},
			modelType:         mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION,
			labels:            singleClassificationMultiLabel,
			minImagesPerLabel: 1, // Lower threshold for this test
			expJSONFile:       filepath.Join(singleLabelDirName, "fakedata_single_label_multi.jsonl"),
		},
		{
			name: "Multiple specified labels for multi label classification " +
				"results in file with list of classification_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeMultiLabelData1, fakeMultiLabelData2, fakeMultiLabelData3,
				fakeMultiLabelData4, fakeMultiLabelData5,
			},
			modelType:         mlv1.ModelType_MODEL_TYPE_MULTI_LABEL_CLASSIFICATION,
			labels:            multiClassificationLabels,
			minImagesPerLabel: 1, // Lower threshold for this test
			expJSONFile:       filepath.Join(multiLabelDirName, "fakedata_multi_label.jsonl"),
		},
		{
			name: "Multiple specified labels for object detection " +
				"results in file with list of bounding_box_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeObjDetectionData1, fakeObjDetectionData2, fakeObjDetectionData3,
			},
			modelType:                mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:                   objectDetectionLabels,
			minBBoxesPerLabel:        1, // Lower threshold for this test
			minImagesObjectDetection: 1, // Lower threshold for this test
			expJSONFile:              filepath.Join(objDetectionDirName, "fakedata_detection.jsonl"),
		},
		{
			name: "No specified labels for custom training " +
				"results in file with list of labels and bounding_box_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeCustomData4, fakeCustomData5,
			},
			modelType:   mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:      nil,
			expJSONFile: filepath.Join(customTrainingDirName, "fakedata_custom_training.jsonl"),
		},
		{
			name: "Too few images for object detection model " +
				"results in an error",
			imageMetadata: []*ImageMetadata{
				fakeObjDetectionData1, fakeObjDetectionData2, fakeObjDetectionData3,
			},
			minImagesObjectDetection: 4,
			modelType:                mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:                   objectDetectionLabels,
			expJSONFile:              filepath.Join(objDetectionDirName, "fakedata_detection.jsonl"),
			expectedErr:              ErrDatasetTooSmall("object detection", 4),
		},
		{
			name:                    "Too few images per class in single-label classification results in an error",
			imageMetadata:           []*ImageMetadata{fakeData1, fakeData2, fakeData3},
			modelType:               mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION,
			labels:                  singleClassificationLabel,
			minBBoxesPerLabel:       10,
			minImagesPerLabel:       10,
			maxRatioUnlabeledImages: .2,
			expJSONFile:             filepath.Join(singleLabelDirName, "fakedata_single_label_binary.jsonl"),
			expectedErr:             ErrTooFewAnnotations("images", singleClassificationLabel, 10),
		},
		{
			name: "A multi-label classification model with 1 image per label results in an error",
			imageMetadata: []*ImageMetadata{
				fakeMultiLabelData1, fakeMultiLabelData2, fakeMultiLabelData3,
				fakeMultiLabelData4, fakeMultiLabelData5,
			},
			modelType:               mlv1.ModelType_MODEL_TYPE_MULTI_LABEL_CLASSIFICATION,
			labels:                  multiClassificationLabels,
			minBBoxesPerLabel:       10,
			minImagesPerLabel:       10,
			maxRatioUnlabeledImages: .2,
			expJSONFile:             multiLabelDirName + "fakedata_multi_label.jsonl",
			expectedErr:             ErrTooFewAnnotations("images", multiClassificationLabels, 10),
		},
		{
			name: "Too few bounding boxes per class in an object detection model results in error",
			imageMetadata: []*ImageMetadata{
				fakeObjDetectionData1, fakeObjDetectionData2, fakeObjDetectionData3,
			},
			modelType:                mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:                   objectDetectionLabels,
			minBBoxesPerLabel:        10,
			minImagesPerLabel:        10,
			minImagesObjectDetection: 3, // Set to 3 so dataset size check passes, then bbox count check fails
			maxRatioUnlabeledImages:  .2,
			expJSONFile:              objDetectionDirName + "fakedata_detection.jsonl",
			expectedErr:              ErrTooFewAnnotations("bounding boxes", objectDetectionLabels, 10),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Save original values
			origMinBBoxesPerLabel := minBBoxesPerLabel
			origMinImagesPerLabel := minImagesPerLabel
			origMaxRatioUnlabeledImages := maxRatioUnlabeledImages
			origMinImagesObjectDetection := minImagesObjectDetection

			// Set test-specific values if provided
			if tc.minBBoxesPerLabel != 0 {
				minBBoxesPerLabel = tc.minBBoxesPerLabel
			}
			if tc.minImagesPerLabel != 0 {
				minImagesPerLabel = tc.minImagesPerLabel
			}
			if tc.maxRatioUnlabeledImages != 0 {
				maxRatioUnlabeledImages = tc.maxRatioUnlabeledImages
			}
			if tc.minImagesObjectDetection != 0 {
				minImagesObjectDetection = tc.minImagesObjectDetection
			}

			// Restore original values after test
			defer func() {
				minBBoxesPerLabel = origMinBBoxesPerLabel
				minImagesPerLabel = origMinImagesPerLabel
				maxRatioUnlabeledImages = origMaxRatioUnlabeledImages
				minImagesObjectDetection = origMinImagesObjectDetection
			}()

			wc := newMockWriter()
			err := ImageMetadataToJSONLines(tc.imageMetadata, tc.labels, tc.modelType, wc)

			if tc.expectedErr == nil {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, wc.closed, test.ShouldBeTrue)

				// Read pre-written test JSON file from artifacts
				file, err := os.Open(artifact.MustPath(tc.expJSONFile))
				test.That(t, err, test.ShouldBeNil)
				defer file.Close()

				fi, err := file.Stat()
				test.That(t, err, test.ShouldBeNil)
				expFileBytes := make([]byte, fi.Size())
				_, err = file.Read(expFileBytes)
				test.That(t, err, test.ShouldBeNil)

				normalizedActual := normalizeJSON(t, wc.buf.String())
				normalizedExpected := normalizeJSON(t, string(expFileBytes))

				test.That(t, normalizedActual, test.ShouldResemble, normalizedExpected)
			} else {
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err, test.ShouldBeError, tc.expectedErr)
			}
		})
	}
}

func normalizeJSON(t *testing.T, jsonString string) string {
	t.Helper()

	// Split the string by lines
	lines := strings.Split(strings.TrimSpace(jsonString), "\n")
	result := []string{}

	for _, line := range lines {
		var obj map[string]any
		err := json.Unmarshal([]byte(line), &obj)
		test.That(t, err, test.ShouldBeNil)

		// Sort the classification_annotations array by annotation_label
		if classifications, ok := obj["classification_annotations"].([]any); ok {
			sort.Slice(classifications, func(i, j int) bool {
				iLabel := classifications[i].(map[string]any)["annotation_label"].(string)
				jLabel := classifications[j].(map[string]any)["annotation_label"].(string)
				return iLabel < jLabel
			})
		}

		// Also sort bounding box annotations if needed
		if boundingBoxes, ok := obj["bounding_box_annotations"].([]any); ok {
			sort.Slice(boundingBoxes, func(i, j int) bool {
				iLabel := boundingBoxes[i].(map[string]any)["annotation_label"].(string)
				jLabel := boundingBoxes[j].(map[string]any)["annotation_label"].(string)
				return iLabel < jLabel
			})
		}

		// Convert back to JSON string
		normalizedBytes, err := json.Marshal(obj)
		test.That(t, err, test.ShouldBeNil)
		result = append(result, string(normalizedBytes))
	}

	return strings.Join(result, "\n") + "\n"
}

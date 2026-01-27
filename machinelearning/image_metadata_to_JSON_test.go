package machine_learning

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	datav1 "go.viam.com/api/app/data/v1"
	mlv1 "go.viam.com/api/app/mltraining/v1"
	"go.viam.com/test"

	"go.viam.com/utils/artifact"
)

var (
	jpegFileExt = ".jpeg"
	pngFileExt  = ".png"
	gifFileExt  = ".gif"
	zipExt      = ".gz"

	singleLabelDirName    = "test_fake_bucket/single"
	multiLabelDirName     = "test_fake_bucket/multi"
	objDetectionDirName   = "test_fake_bucket/detection"
	customTrainingDirName = "test_fake_bucket/custom"

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
		Tags:   []string{"cat"},
		Bucket: singleLabelDirName,
		Path:   "filename1.jpeg" + zipExt,
	}
	fakeData2 = &ImageMetadata{
		Tags:   []string{"cat"},
		Bucket: singleLabelDirName,
		Path:   "filename2.jpeg" + zipExt,
	}
	fakeData3 = &ImageMetadata{
		Tags:   []string{"dog"},
		Bucket: singleLabelDirName,
		Path:   "filename3.jpeg" + zipExt,
	}
	fakeData4 = &ImageMetadata{
		Tags:   []string{"turtle"},
		Bucket: singleLabelDirName,
		Path:   "filename4.jpeg" + zipExt,
	}
	fakeData5 = &ImageMetadata{
		Tags:   []string{"penguin"},
		Bucket: singleLabelDirName,
		Path:   "filename5.jpeg" + zipExt,
	}

	fakeMultiLabelData1 = &ImageMetadata{
		Tags:   []string{"daisy", "full_shot"},
		Bucket: multiLabelDirName,
		Path:   "filename1.jpeg" + zipExt,
	}
	fakeMultiLabelData2 = &ImageMetadata{
		Tags:   []string{"dandelion", "medium_shot"},
		Bucket: multiLabelDirName,
		Path:   "filename2" + gifFileExt + zipExt,
	}
	fakeMultiLabelData3 = &ImageMetadata{
		Tags:   []string{"roses", "extreme_closeup"},
		Bucket: multiLabelDirName,
		Path:   "filename3" + pngFileExt + zipExt,
	}
	fakeMultiLabelData4 = &ImageMetadata{
		Tags:   []string{"sunflowers", "closeup"},
		Bucket: multiLabelDirName,
		Path:   "filename4" + jpegFileExt + zipExt,
	}
	fakeMultiLabelData5 = &ImageMetadata{
		Tags:   []string{"tulips", "extreme_closeup"},
		Bucket: multiLabelDirName,
		Path:   "filename5" + pngFileExt + zipExt,
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
		Bucket: objDetectionDirName,
		Path:   "filename1" + pngFileExt + zipExt,
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
		Bucket: objDetectionDirName,
		Path:   "filename2" + jpegFileExt + zipExt,
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
		Bucket: objDetectionDirName,
		Path:   "filename3" + jpegFileExt + zipExt,
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
	}

	fakeCustomDataEmptyBucket = &ImageMetadata{
		Tags: []string{"cat"},
		Annotations: &datav1.Annotations{
			Bboxes: []*datav1.BoundingBox{
				{
					Id:             "8",
					Label:          "cat",
					XMinNormalized: 0.1,
					XMaxNormalized: 0.15,
					YMinNormalized: 0.2,
					YMaxNormalized: 0.25,
				},
			},
		},
		Bucket:        "",
		Path:          "/local/path/filename6.jpeg",
		PartID:        "part2",
		ComponentName: "component3",
	}
)

// mockWriter implements CloseableWriter for testing.
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
		expectedLabelsCount      map[string]int32
		expectedImageCount       int
		expectedMultiLabelCount  int
	}{
		{
			name: "Only one specified label for single label classification " +
				"results in file with one classification_annotation with string label and UNSPECIFIED others",
			imageMetadata:           []*ImageMetadata{fakeData1, fakeData2, fakeData3},
			modelType:               mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION,
			labels:                  singleClassificationLabel,
			maxRatioUnlabeledImages: .4,
			expJSONFile:             filepath.Join(singleLabelDirName, "fakedata_single_label_binary.jsonl"),
			expectedLabelsCount: map[string]int32{
				"cat":        2,
				UnknownLabel: 1,
			},
			expectedImageCount:      3,
			expectedMultiLabelCount: 0,
		},
		{
			name: "Multiple specified labels for single label classification " +
				"results in file with only one classification_annotation per image",
			imageMetadata: []*ImageMetadata{
				fakeData1, fakeData2, fakeData3, fakeData4, fakeData5,
			},
			modelType:   mlv1.ModelType_MODEL_TYPE_SINGLE_LABEL_CLASSIFICATION,
			labels:      singleClassificationMultiLabel,
			expJSONFile: filepath.Join(singleLabelDirName, "fakedata_single_label_multi.jsonl"),
			expectedLabelsCount: map[string]int32{
				"cat":     2,
				"dog":     1,
				"turtle":  1,
				"penguin": 1,
			},
			expectedImageCount:      5,
			expectedMultiLabelCount: 0,
		},
		{
			name: "Multiple specified labels for multi label classification " +
				"results in file with list of classification_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeMultiLabelData1, fakeMultiLabelData2, fakeMultiLabelData3,
				fakeMultiLabelData4, fakeMultiLabelData5,
			},
			modelType:   mlv1.ModelType_MODEL_TYPE_MULTI_LABEL_CLASSIFICATION,
			labels:      multiClassificationLabels,
			expJSONFile: filepath.Join(multiLabelDirName, "fakedata_multi_label.jsonl"),
			expectedLabelsCount: map[string]int32{
				"daisy":           1,
				"dandelion":       1,
				"roses":           1,
				"sunflowers":      1,
				"tulips":          1,
				"medium_shot":     1,
				"full_shot":       1,
				"closeup":         1,
				"extreme_closeup": 2,
			},
			expectedImageCount:      5,
			expectedMultiLabelCount: 0,
		},
		{
			name: "Multiple specified labels for object detection " +
				"results in file with list of bounding_box_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeObjDetectionData1, fakeObjDetectionData2, fakeObjDetectionData3,
			},
			modelType:   mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:      objectDetectionLabels,
			expJSONFile: filepath.Join(objDetectionDirName, "fakedata_detection.jsonl"),
			expectedLabelsCount: map[string]int32{
				"cat": 3,
				"dog": 2,
			},
			expectedImageCount:      3,
			expectedMultiLabelCount: 0,
		},
		{
			name: "No specified labels for custom training " +
				"results in file with list of labels and bounding_box_annotations per image",
			imageMetadata: []*ImageMetadata{
				fakeCustomData4, fakeCustomData5,
			},
			modelType:               mlv1.ModelType_MODEL_TYPE_OBJECT_DETECTION,
			labels:                  nil,
			expJSONFile:             filepath.Join(customTrainingDirName, "fakedata_custom_training.jsonl"),
			expectedLabelsCount:     map[string]int32{},
			expectedImageCount:      2,
			expectedMultiLabelCount: 0,
		},
		{
			name: "Empty bucket for custom training " +
				"results in path without /gcs prefix",
			imageMetadata: []*ImageMetadata{
				fakeCustomDataEmptyBucket,
			},
			modelType:               mlv1.ModelType_MODEL_TYPE_UNSPECIFIED,
			labels:                  nil,
			expJSONFile:             filepath.Join(customTrainingDirName, "fakedata_empty_bucket.jsonl"),
			expectedLabelsCount:     map[string]int32{},
			expectedImageCount:      1,
			expectedMultiLabelCount: 0,
		},
		{
			name: "Model type unspecified with requested labels " +
				"results in error",
			imageMetadata: []*ImageMetadata{
				fakeData1, fakeData2,
			},
			modelType:               mlv1.ModelType_MODEL_TYPE_UNSPECIFIED,
			labels:                  singleClassificationLabel,
			expJSONFile:             "",
			expectedErr:             errors.New("model type not specified"),
			expectedLabelsCount:     nil,
			expectedImageCount:      0,
			expectedMultiLabelCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wc := newMockWriter()
			labelCountsResult, err := ImageMetadataToJSONLines(tc.imageMetadata, tc.labels, tc.modelType, wc)

			if tc.expectedErr == nil {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, labelCountsResult.LabelCounts, test.ShouldResemble, tc.expectedLabelsCount)
				test.That(t, labelCountsResult.DatasetSize, test.ShouldEqual, tc.expectedImageCount)
				test.That(t, labelCountsResult.MultiLabelCount, test.ShouldEqual, tc.expectedMultiLabelCount)

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

				test.That(t, labelCountsResult.LabelCounts, test.ShouldResemble, tc.expectedLabelsCount)
				test.That(t, labelCountsResult.DatasetSize, test.ShouldEqual, tc.expectedImageCount)
				test.That(t, labelCountsResult.MultiLabelCount, test.ShouldEqual, tc.expectedMultiLabelCount)
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

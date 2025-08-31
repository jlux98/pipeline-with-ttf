package tasktestrun

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	_ "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/tasktestrun/fake"
	ttesting "github.com/tektoncd/pipeline/pkg/reconciler/testing"
	"github.com/tektoncd/pipeline/test"
	"github.com/tektoncd/pipeline/test/diff"
	"github.com/tektoncd/pipeline/test/names"
	"github.com/tektoncd/pipeline/test/parse"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	cminformer "knative.dev/pkg/configmap/informer"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"
	_ "knative.dev/pkg/system/testing" // Setup system.Namespace()
)

var now = time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
var testClock = clock.NewFakePassiveClock(now)

// IgnoreFields options
var (
	ignoreResourceVersion    = cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")
	ignoreLastTransitionTime = cmpopts.IgnoreFields(
		apis.Condition{},
		"LastTransitionTime.Inner.Time",
	)
	ignoreTaskRunStatus = cmpopts.IgnoreFields(
		v1alpha1.TaskTestRunStatusFields{},
		"TaskRunStatus",
	)
	ignoreStartTimeTaskRun     = cmpopts.IgnoreFields(v1.TaskRunStatusFields{}, "StartTime")
	ignoreStartTimeTaskTestRun = cmpopts.IgnoreFields(
		v1alpha1.TaskTestRunStatusFields{},
		"StartTime",
	)
	ignoreCompletionTimeTaskTestRun = cmpopts.IgnoreFields(
		v1alpha1.TaskTestRunStatusFields{},
		"CompletionTime",
	)
)

// Task manifests
const (
	tManifest = `
metadata:
  name: task
  namespace: foo
spec:
  params:
  - name: args
    default: ""
  steps:
  - name: simple-step
    command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
  - name: another-simple-step
    command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
`

	tManifestHelloTask = `
metadata:
  name: hello-task
  namespace: foo
spec:
  results:
  - description: |
      The current time in the format hh:mm:ss
    name: current-time
    type: string
  - description: |
      The current date in the format dd:mm:yy
    name: current-date
    type: string
  params:
  - name: args
    default: ""
  workspaces:
  - name: hello-workspace
  steps:
  - computeResources: {}
    image: alpine
    name: date-step
    script: |
      echo "Hello world!"
      date +%Y-%m-%d | tee $(results.current-date.path)
  - computeResources: {}
    image: alpine
    name: time-step
    script: |
      echo "Hello world!"
      date \+%H:%M:%S | tee $(results.current-time.path)
`
)

// TaskTest manifests
const ttManifest = `
metadata:
  name: task-test
  namespace: foo
spec:
  taskRef:
    name: hello-task
  inputs:
    params:
    - name: "args"
      value: "arg"
    env:
    - name: FOO
      value: bar
    stepEnvs:
    - stepName: date-step
      env:
      - name: ANOTHER_FOO
        value: ANOTHER_BAR
    workspaceContents:
    - name: hello-workspace
      objects:
      - path: test/foo
        type: TextFile
        content: bar
      - path: /test/dir
        type: Directory
  expects:
    successStatus: true
    successReason: Succeeded
    results:
    - name: current-date
      type: string
      value: "2025-08-15"
    - name: current-time
      type: string
      value: "15:17:59"
    env:
    - name: HOME
      value: "/root"
    stepEnvs:
    - stepName: time-step
      env:
      - name: FHOME
        value: "/froot"
    fileSystemContents:
    - stepName: date-step
      objects:
      - path: /tekton/results/current-date
        type: TextFile
        content: bar
    - stepName: time-step
      objects:
      - path: /tekton/results/current-time
        type: TextFile
        content: bar
`

// // Invalid TaskTestRun manifests
// const (
//     ttrManifestAbsentTaskTest = `
// metadata:
//   name: invalid-ttr-absent-task-test
//   namespace: foo
// spec:
//   taskTestRef:
//     name: absent-task-test`

//     ttrManifestExpectsUndeclaredResult = `
// metadata:
//   name: invalid-ttr-expects-undeclared-result
//   namespace: foo
// spec:
//   timeout: 1h
//   taskTestSpec:
//     taskRef:
//       name: task
//     expects:
//       results:
//       - name: current-date
//         type: string
//         value: "2025-08-15"
//       - name: current-time
//         type: string
//         value: "15:17:59"
//       successStatus: true
//       successReason: Succeeded
// status:
//   startTime: %s`

//     ttrManifestInputsUndeclaredParam = `
// metadata:
//   name: invalid-ttr-inputs-undeclared-param
//   namespace: foo
// spec:
//   timeout: 1h
//   taskTestSpec:
//     taskRef:
//       name: task
//     inputs:
//       params:
//       - name: foo
//         value: bar
// status:
//   startTime: %s`

//     ttrManifestInputsUndeclaredStepEnvStep = `
// metadata:
//   name: invalid-ttr-inputs-undeclared-step-env-step
//   namespace: foo
// spec:
//   timeout: 1h
//   taskTestSpec:
//     taskRef:
//       name: task
//     inputs:
//       stepEnvs:
//       - stepName: goodbye-step
//         env:
//         - name: FOO
//           value: BAR
// status:
//   startTime: %s`

//     ttrManifestExpectsUndeclaredEnvStep = `
// metadata:
//   name: invalid-ttr-expects-undeclared-env-step
//   namespace: foo
// spec:
//   timeout: 1h
//   taskTestSpec:
//     taskRef:
//       name: task
//     expects:
//       stepEnvs:
//       - stepName: goodbye-step
//         env:
//         - name: HOME
//           value: /root
//       successStatus: true
//       successReason: Succeeded
// status:
//   startTime: %s`

//     ttrManifestExpectsUndeclaredFileSystemStep = `
// metadata:
//   name: invalid-ttr-expects-undeclared-fs-step
//   namespace: foo
// spec:
//   timeout: 1h
//   taskTestSpec:
//     taskRef:
//       name: task
//     expects:
//       fileSystemContents:
//       - stepName: goodbye-step
//         objects:
//         - path: /tekton/results/current-date
//           type: Directory
//         - path: /tekton/results/current-time
//           type: TextFile
//           content: foo
//       successStatus: true
//       successReason: Succeeded
// status:
//   startTime: %s`

//     ttrManifestAbsentTask = `
// metadata:
//   name: invalid-ttr-absent-task
//   namespace: foo
// spec:
//   taskTestSpec:
//     taskRef:
//       name: absent-task`
// )

// TaskTestRun Templates
const trSpecTemplate = `
metadata:
  name: %s # TaskTestsRun name
  namespace: foo
  annotations:
    #                    {"results":[{"name":"current-date","type":"string","value":"2025-08-15"},{"name":"current-time","type":"string","value":"15:17:59"}],"env":[{"name":"HOME","value":"/root"}],"successStatus":true,"successReason":"Succeeded","fileSystemContents":[{"stepName":"date-step","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"stepName":"time-step","objects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]}
    ExpectedValuesJSON: '{"results":[{"name":"current-date","type":"string","value":"2025-08-15"},{"name":"current-time","type":"string","value":"15:17:59"}],"env":[{"name":"HOME","value":"/root"}],"stepEnvs":[{"stepName":"time-step","env":[{"name":"FHOME","value":"/froot"}]}],"successStatus":true,"successReason":"Succeeded","fileSystemContents":[{"stepName":"date-step","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"stepName":"time-step","objects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]}'
  labels:
    tekton.dev/taskTestRun: %s # TaskTestsRun name
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: %s # TaskTestsRun name
    controller: true
    blockOwnerDeletion: true
spec:
  taskSpec:
    results:
    - description: |
        The current time in the format hh:mm:ss
      name: current-time
      type: string
    - description: |
        The current date in the format dd:mm:yy
      name: current-date
      type: string
    params:
    - name: args
      default: ""
    workspaces:
    - name: hello-workspace
    steps:
    - computeResources: {}
      name: prepare-workspace
      image: busybox:1.37.0
      command: ["sh", "-c"]
      args:
      - |
          mkdir -p $(workspaces.hello-workspace.path)/test
          printf "%%s" "bar" > $(workspaces.hello-workspace.path)/test/foo
          mkdir -p $(workspaces.hello-workspace.path)/test/dir
    - computeResources: {}
      env:
      - name: ANOTHER_FOO
        value: ANOTHER_BAR
      image: alpine
      name: date-step
      script: |
        echo "Hello world!"
        date +%%Y-%%m-%%d | tee $(results.current-date.path)
    - computeResources: {}
      image: alpine
      name: time-step
      script: |
        echo "Hello world!"
        date \+%%H:%%M:%%S | tee $(results.current-time.path)
  params:
  - name: "args"
    value: "arg"`

const trStatusRunning = `
status:
  conditions:
    - reason: Started
      status: Unknown
      type: Succeeded
  startTime: "2025-08-15T15:17:55Z"
`

const trStatusFinished = `
status:
  completionTime: "2025-08-15T15:17:59Z"
  conditions:
  - message: All Steps have completed executing
    reason: Succeeded
    status: "True"
    type: Succeeded
  podName: hello-world-run-b6b5k-pod
  results:
  - name: current-date
    type: string
    value: 2025-08-15
  - name: current-time
    type: string
    value: 15:17:59
  - name: Testing|Environment
    type: string
    value: |
      {"step": "date-step", "environment": {
      "HOME=/root",
      }},
      {"step": "time-step", "environment": {
        "FHOME=/froot",
        "HOME=/root",
      }}
  - name: Testing|FileSystemContent
    type: string
    value: '[{"stepName":"/tekton/run/0/status","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"stepName":"/tekton/run/1/status","objects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]'
  startTime: "2025-08-15T15:17:55Z"
  taskSpec:
    results:
    - description: |
        The current time in the format hh:mm:ss
      name: current-time
      type: string
    - description: |
        The current date in the format dd:mm:yy
      name: current-date
      type: string
    params:
    - name: args
      default: ""
    steps:
    - computeResources: {}
      image: alpine
      name: date-step
      script: |
        echo "Hello world!"
        date +%%Y-%%m-%%d | tee /tekton/results/current-date
    - computeResources: {}
      image: alpine
      name: time-step
      script: |
        echo "Hello world!"
        date \+%%H:%%M:%%S | tee /tekton/results/current-time
`

// TaskTestRun Templates
const ttrSpecTemplateInlineTest = `
metadata:
  name: %s
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    inputs:
      params:
      - name: "args"
        value: "arg"
      env:
      - name: FOO
        value: bar
      stepEnvs:
      - stepName: date-step
        env:
        - name: ANOTHER_FOO
          value: ANOTHER_BAR
      workspaceContents:
      - name: hello-workspace
        objects:
        - path: test/foo
          type: TextFile
          content: bar
        - path: /test/dir
          type: Directory
    expects:
      successStatus: true
      successReason: Succeeded
      results:
      - name: current-date
        type: string
        value: "2025-08-15"
      - name: current-time
        type: string
        value: "15:17:59"
      env:
      - name: HOME
        value: "/root"
      fileSystemContents:
      - stepName: date-step
        objects:
        - path: /tekton/results/current-date
          type: TextFile
          content: bar
      - stepName: time-step
        objects:
        - path: /tekton/results/current-time
          type: TextFile
          content: bar
      stepEnvs:
      - stepName: time-step
        env:
        - name: FHOME
          value: "/froot"
`

const ttrSpecTemplateInlineTestInaccurateExpectations = `
metadata:
  name: %s
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: hello-task
    inputs:
      params:
      - name: "args"
        value: "arg"
      env:
      - name: FOO
        value: bar
      stepEnvs:
      - stepName: date-step
        env:
        - name: ANOTHER_FOO
          value: ANOTHER_BAR
    expects:
      successStatus: false
      successReason: Failed
      results:
      - name: current-date
        type: string
        value: "2015-08-15"
      - name: current-time
        type: string
        value: "05:17:59"
      env:
      - name: HOME
        value: "/groot"
      fileSystemContents:
      - stepName: date-step
        objects:
        - path: /tekton/results/current-date
          type: Directory
      - stepName: time-step
        objects:
        - path: /tekton/results/current-time
          type: TextFile
          content: foo
      stepEnvs:
      - stepName: time-step
        env:
        - name: FHOME
          value: "/froot"
`

const ttrSpecTemplateReferencedTest = `
metadata:
  name: %s
  namespace: foo
spec:
  taskTestRef:
    name: task-test
`

const ttrStatusRunning = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown
`
const ttrStatusToBeRetried = `
status:
  conditions:
  - type: Succeeded
    reason: ToBeRetried
    status: Unknown
  retriesStatus:
  - conditions:
    - type: Succeeded
      reason: TaskTestRunUnexpectedOutcomes
      status: "False"
`

const ttrStatusCompletedSuccessful = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    status: "True"
    reason: Succeeded
    message: TaskRun completed executing and outcomes were as expected
`

const ttrStatusCompletedFailed = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    status: "False"
    reason: TaskTestRunUnexpectedOutcomes
    message: not all expectations were met
  taskRunName : %s
`

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	const (
		tcStartNewRunInlineTest                     = "start-new-run-inline-test"
		tcCheckRunningInlineTest                    = "check-running-inline-test"
		tcCheckCompletedSuccessfulInlineTest        = "check-completed-successful-inline-test"
		tcCheckCompletedSuccessfulReferencedTest    = "check-completed-successful-referenced-test"
		tcCheckCompletedFailedInlineTestNoRetries   = "check-completed-failed-inline-test-no-retries"
		tcCheckCompletedFailedInlineTestWithRetries = "check-completed-failed-inline-test-with-retries"
		tcStartRetryInlineTest                      = "start-retry-inline-test"
	)

	// fill maps
	taskRunMap := map[string]*v1.TaskRun{
		tcCheckRunningInlineTest:                    generateTaskRun(t, trSpecTemplate+trStatusRunning, tcCheckRunningInlineTest),
		tcCheckCompletedSuccessfulInlineTest:        generateTaskRun(t, trSpecTemplate+trStatusFinished, tcCheckCompletedSuccessfulInlineTest),
		tcCheckCompletedSuccessfulReferencedTest:    generateTaskRun(t, trSpecTemplate+trStatusFinished, tcCheckCompletedSuccessfulReferencedTest),
		tcCheckCompletedFailedInlineTestNoRetries:   generateTaskRun(t, trSpecTemplate+trStatusFinished, tcCheckCompletedFailedInlineTestNoRetries),
		tcCheckCompletedFailedInlineTestWithRetries: generateTaskRun(t, trSpecTemplate+trStatusFinished, tcCheckCompletedFailedInlineTestWithRetries, 0),
	}

	taskTestMap := map[string]*v1alpha1.TaskTest{
		tcCheckCompletedSuccessfulReferencedTest: parse.MustParseTaskTest(t, ttManifest),
	}

	taskTestRunMap := map[string]*v1alpha1.TaskTestRun{
		tcStartNewRunInlineTest:                     parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest, tcStartNewRunInlineTest)),
		tcCheckRunningInlineTest:                    parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest, tcCheckRunningInlineTest)),
		tcCheckCompletedSuccessfulInlineTest:        parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest, tcCheckCompletedSuccessfulInlineTest)),
		tcCheckCompletedSuccessfulReferencedTest:    parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateReferencedTest, tcCheckCompletedSuccessfulReferencedTest)),
		tcCheckCompletedFailedInlineTestNoRetries:   parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTestInaccurateExpectations, tcCheckCompletedFailedInlineTestNoRetries)),
		tcCheckCompletedFailedInlineTestWithRetries: parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTestInaccurateExpectations+"\n  retries: 1"+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedInlineTestWithRetries+"-run-0"), tcCheckCompletedFailedInlineTestWithRetries)),
		tcStartRetryInlineTest:                      parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest+"\n  retries: 1"+ttrStatusToBeRetried, tcStartRetryInlineTest)),
	}

	// load custom resources into data for the fake cluster
	data := test.Data{
		Tasks: []*v1.Task{
			parse.MustParseV1Task(t, tManifest),
			parse.MustParseV1Task(t, tManifestHelloTask),
		},
		TaskRuns:     slices.Collect(maps.Values(taskRunMap)),
		TaskTests:    slices.Collect(maps.Values(taskTestMap)),
		TaskTestRuns: slices.Collect(maps.Values(taskTestRunMap)),
	}

	// declare some diffs that will be used in multiple places
	diffCurrentDate := cmp.Diff(&v1.ResultValue{
		Type:      "string",
		StringVal: "2015-08-15",
	}, &v1.ResultValue{
		Type:      "string",
		StringVal: "2025-08-15",
	})
	diffCurrentTime := cmp.Diff(&v1.ResultValue{
		Type:      "string",
		StringVal: "05:17:59",
	}, &v1.ResultValue{
		Type:      "string",
		StringVal: "15:17:59",
	})
	diffEnv := cmp.Diff("/groot", "/root")
	diffType := cmp.Diff(v1alpha1.DirectoryType, v1alpha1.TextFileType)
	diffContent := cmp.Diff("foo", "bar")

	type tc struct {
		ttr                *v1alpha1.TaskTestRun
		wantTtrStatus      *v1alpha1.TaskTestRunStatus
		wantTr             *v1.TaskRun
		wantStartTime      bool
		wantCompletionTime bool
	}
	tests := map[string]tc{
		tcStartNewRunInlineTest: {
			ttr: taskTestRunMap[tcStartNewRunInlineTest],
			wantTtrStatus: patchTaskTestRun(parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest+ttrStatusRunning, tcStartNewRunInlineTest)), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.TaskRunName = ptr.To(tcStartNewRunInlineTest + "-run")
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        generateTaskRun(t, trSpecTemplate+trStatusRunning, tcStartNewRunInlineTest),
			wantStartTime: true,
		},
		tcCheckRunningInlineTest: {
			ttr: taskTestRunMap[tcCheckRunningInlineTest],
			wantTtrStatus: patchTaskTestRun(
				parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest+ttrStatusRunning, tcCheckRunningInlineTest)),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskRunName = ptr.To(tcCheckRunningInlineTest + "-run")
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
				}),
			wantTr:        taskRunMap[tcCheckRunningInlineTest],
			wantStartTime: true,
		},
		tcCheckCompletedSuccessfulInlineTest: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessfulInlineTest],
			wantTtrStatus: patchTaskTestRun(
				parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest+ttrStatusCompletedSuccessful, tcCheckCompletedSuccessfulInlineTest)),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
					ttr.Status.TaskRunName = ptr.To(tcCheckCompletedSuccessfulInlineTest + "-run")
					ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{
						Results: &[]v1alpha1.ObservedResults{{
							Name: "current-date",
							Want: &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
							Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
						}, {
							Name: "current-time",
							Want: &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
							Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
						}},
						StepEnvs: &[]v1alpha1.ObservedStepEnv{{
							StepName: "date-step",
							Env: []v1alpha1.ObservedEnvVar{{
								Name: "HOME",
								Want: "/root",
								Got:  "/root",
							}}}, {
							StepName: "time-step",
							Env: []v1alpha1.ObservedEnvVar{{
								Name: "FHOME",
								Want: "/froot",
								Got:  "/froot",
							}, {
								Name: "HOME",
								Want: "/root",
								Got:  "/root",
							}},
						}},
						FileSystemObjects: ptr.To([]v1alpha1.ObservedStepFileSystemContent{{
							StepName: "date-step",
							Objects: []v1alpha1.ObservedFileSystemObject{{
								Path:        "/tekton/results/current-date",
								WantType:    "TextFile",
								GotType:     "TextFile",
								WantContent: "bar",
								GotContent:  "bar",
							}},
						}, {
							StepName: "time-step",
							Objects: []v1alpha1.ObservedFileSystemObject{{
								Path:        "/tekton/results/current-time",
								WantType:    "TextFile",
								GotType:     "TextFile",
								WantContent: "bar",
								GotContent:  "bar",
							}},
						}}),
						SuccessStatus: &v1alpha1.ObservedSuccessStatus{Want: true, Got: true},
						SuccessReason: &v1alpha1.ObservedSuccessReason{
							Want: v1.TaskRunReasonSuccessful,
							Got:  v1.TaskRunReasonSuccessful,
						},
					}
				},
			),
			wantTr:             taskRunMap[tcCheckCompletedSuccessfulInlineTest],
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckCompletedSuccessfulReferencedTest: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessfulReferencedTest],
			wantTtrStatus: patchTaskTestRun(
				parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTest+ttrStatusCompletedSuccessful, tcCheckCompletedSuccessfulReferencedTest)),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "True",
						Reason:  "Succeeded",
						Message: "TaskRun completed executing and outcomes were as expected",
					}}
					ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
						TaskRunName:  ptr.To(tcCheckCompletedSuccessfulReferencedTest + "-run"),
						TaskTestName: ptr.To("task-test"),
						TaskTestSpec: &taskTestMap[tcCheckCompletedSuccessfulReferencedTest].Spec,
						Outcomes: &v1alpha1.ObservedOutcomes{
							Results: &[]v1alpha1.ObservedResults{{Name: "current-date",
								Want: &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
								Got: &v1.ResultValue{
									Type:      "string",
									StringVal: "2025-08-15",
								}}, {Name: "current-time",
								Want: &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
								Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
							}},
							StepEnvs: &[]v1alpha1.ObservedStepEnv{{
								StepName: "date-step",
								Env: []v1alpha1.ObservedEnvVar{{
									Name: "HOME",
									Want: "/root",
									Got:  "/root",
								}},
							}, {
								StepName: "time-step",
								Env: []v1alpha1.ObservedEnvVar{{
									Name: "FHOME",
									Want: "/froot",
									Got:  "/froot",
								}, {
									Name: "HOME",
									Want: "/root",
									Got:  "/root",
								}},
							}},
							FileSystemObjects: &[]v1alpha1.ObservedStepFileSystemContent{{
								StepName: "date-step",
								Objects: []v1alpha1.ObservedFileSystemObject{{
									Path:        "/tekton/results/current-date",
									WantType:    "TextFile",
									GotType:     "TextFile",
									WantContent: "bar",
									GotContent:  "bar",
								}},
							}, {
								StepName: "time-step",
								Objects: []v1alpha1.ObservedFileSystemObject{{
									Path:        "/tekton/results/current-time",
									WantType:    "TextFile",
									GotType:     "TextFile",
									WantContent: "bar",
									GotContent:  "bar",
								}},
							}},
							SuccessStatus: &v1alpha1.ObservedSuccessStatus{Want: true, Got: true},
							SuccessReason: &v1alpha1.ObservedSuccessReason{
								Want: v1.TaskRunReasonSuccessful,
								Got:  v1.TaskRunReasonSuccessful,
							},
						},
					}
				},
			),
			wantTr:             taskRunMap[tcCheckCompletedSuccessfulReferencedTest],
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckCompletedFailedInlineTestNoRetries: {
			ttr: taskTestRunMap[tcCheckCompletedFailedInlineTestNoRetries],
			wantTtrStatus: patchTaskTestRun(
				parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedInlineTestNoRetries+"-run"), tcCheckCompletedFailedInlineTestNoRetries)),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{}
					ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
						TaskRunName: ptr.To(tcCheckCompletedFailedInlineTestNoRetries + "-run"),
						Outcomes: &v1alpha1.ObservedOutcomes{
							Results: &[]v1alpha1.ObservedResults{{
								Name: "current-date",
								Want: &v1.ResultValue{Type: "string", StringVal: "2015-08-15"},
								Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
								Diff: diffCurrentDate,
							}, {
								Name: "current-time",
								Want: &v1.ResultValue{Type: "string", StringVal: "05:17:59"},
								Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
								Diff: diffCurrentTime,
							}},
							StepEnvs: &[]v1alpha1.ObservedStepEnv{{
								StepName: "date-step",
								Env: []v1alpha1.ObservedEnvVar{{
									Name: "HOME",
									Want: "/groot",
									Got:  "/root",
									Diff: diffEnv,
								}},
							}, {
								StepName: "time-step",
								Env: []v1alpha1.ObservedEnvVar{{
									Name: "FHOME",
									Want: "/froot",
									Got:  "/froot",
								}, {
									Name: "HOME",
									Want: "/groot",
									Got:  "/root",
									Diff: diffEnv,
								}},
							}},
							FileSystemObjects: &[]v1alpha1.ObservedStepFileSystemContent{{
								StepName: "date-step",
								Objects: []v1alpha1.ObservedFileSystemObject{{
									Path:       "/tekton/results/current-date",
									WantType:   v1alpha1.DirectoryType,
									GotType:    v1alpha1.TextFileType,
									DiffType:   diffType,
									GotContent: "bar",
								}},
							}, {
								StepName: "time-step",
								Objects: []v1alpha1.ObservedFileSystemObject{{
									Path:        "/tekton/results/current-time",
									WantType:    v1alpha1.TextFileType,
									GotType:     v1alpha1.TextFileType,
									WantContent: "foo",
									GotContent:  "bar",
									DiffContent: diffContent,
								}},
							}},
							SuccessStatus: &v1alpha1.ObservedSuccessStatus{
								Want:               false,
								Got:                true,
								WantDiffersFromGot: true,
							},
							SuccessReason: &v1alpha1.ObservedSuccessReason{
								Want:               "Failed",
								Got:                "Succeeded",
								WantDiffersFromGot: true,
							},
							Diffs: "observed success status did not match expectation\n" +
								"observed success reason did not match expectation\n" +
								"Result current-date: " + diffCurrentDate +
								"Result current-time: " + diffCurrentTime +
								"envVar HOME in step date-step: " + diffEnv +
								"envVar HOME in step time-step: " + diffEnv +
								"file system object \"/tekton/results/current-date\" type in step date-step: " + diffType +
								"file system object \"/tekton/results/current-time\" content in step time-step: " + diffContent,
						},
					}
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
				},
			),
			wantTr:             taskRunMap[tcCheckCompletedFailedInlineTestNoRetries],
			wantStartTime:      true,
			wantCompletionTime: true,
		},
		tcCheckCompletedFailedInlineTestWithRetries: {
			ttr: taskTestRunMap[tcCheckCompletedFailedInlineTestWithRetries],
			wantTtrStatus: patchTaskTestRun(
				parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateInlineTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedInlineTestWithRetries+"-run-0"), tcCheckCompletedFailedInlineTestWithRetries)),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
						TaskRunName: ptr.To(tcCheckCompletedFailedInlineTestWithRetries + "-run-0"),
					}
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
					statusCopy := *ttr.Status.DeepCopy()
					statusCopy.RetriesStatus = nil
					ttr.Status.RetriesStatus = append(ttr.Status.RetriesStatus, statusCopy)
					ttr.Status.Outcomes = nil
					ttr.Status.TaskRunName = nil
					ttr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "Unknown",
						Reason:  "ToBeRetried",
						Message: "not all expectations were met",
					}}
				},
			),
		},
		tcStartRetryInlineTest: {
			ttr: taskTestRunMap[tcStartRetryInlineTest],
			wantTtrStatus: patchTaskTestRun(taskTestRunMap[tcStartRetryInlineTest].DeepCopy(), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttr.Status.TaskRunName = ptr.To(tcStartRetryInlineTest + "-run-1")
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        generateTaskRun(t, trSpecTemplate+trStatusRunning, tcStartRetryInlineTest, 1),
			wantStartTime: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, data)
			clients := testAssets.Clients
			defer cancel()

			_ = testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))
			// gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))

			// if gotErr != nil {
			//     t.Errorf("Go unexpected error: %v", gotErr)
			// }

			ttr, err := clients.Pipeline.TektonV1alpha1().
				TaskTestRuns(tt.ttr.Namespace).
				Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting updated tasktestrun: %v", err)
			}
			if tt.wantStartTime && ttr.Status.StartTime == nil {
				t.Error("TaskTestRun: Didn't expect start time to be nil")
			}
			if !tt.wantStartTime && ttr.Status.StartTime != nil {
				t.Error("TaskTestRun: Expected start time to be nil")
			}
			if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
				t.Error("TaskTestRun: Didn't expect completion time to be nil")
			}
			if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
				t.Error("TaskTestRun: Expected completion time to be nil")
			}
			if d := cmp.Diff(*tt.wantTtrStatus, ttr.Status,
				ignoreResourceVersion,
				ignoreLastTransitionTime,
				ignoreTaskRunStatus,
				ignoreStartTimeTaskTestRun,
				ignoreCompletionTimeTaskTestRun); d != "" {
				t.Errorf("Didn't get expected TaskTestRun: %v", diff.PrintWantGot(d))
			}

			if ttr.Status.TaskRunName == nil {
				ttr.Status.TaskRunName = ptr.To("")
			}
			tr, err := clients.Pipeline.TektonV1().
				TaskRuns(tt.ttr.Namespace).
				Get(testAssets.Ctx, *ttr.Status.TaskRunName, metav1.GetOptions{})
			if tt.wantTr != nil {
				if err != nil {
					t.Fatalf("getting updated taskrun: %v", err)
				}

				if tt.wantStartTime && tr.Status.StartTime == nil {
					t.Error("TaskRun: Didn't expect start time to be nil")
				}
				if !tt.wantStartTime && tr.Status.StartTime != nil {
					t.Error("TaskRun: Expected start time to be nil")
				}
				if tt.wantCompletionTime && tr.Status.CompletionTime == nil {
					t.Error("TaskRun: Didn't expect completion time to be nil")
				}
				if !tt.wantCompletionTime && tr.Status.CompletionTime != nil {
					t.Error("TaskRun: Expected completion time to be nil")
				}
				if d := cmp.Diff(tt.wantTr, tr,
					ignoreResourceVersion,
					ignoreStartTimeTaskRun,
					ignoreLastTransitionTime); d != "" {
					t.Errorf("Didn't get expected TaskRun: %v", diff.PrintWantGot(d))
				}
				if d := cmp.Diff(&tr.Status, ttr.Status.TaskRunStatus); d != "" {
					t.Errorf(
						"TaskRun Status not mirrored properly to TaskTestRun: %v",
						diff.PrintWantGot(d),
					)
				}
			} else {
				if err != nil {
					if !k8serrors.IsNotFound(err) {
						t.Fatalf("getting updated taskrun: %v", err)
					}
				} else {
					if tr != nil {
						t.Fatalf("expected no taskrun but got:\n\n%v", tr)
					}
				}
			}
		})
	}
}

// func TestReconciler_InvalidateReconcileKind(t *testing.T) {
//     trCompletedNoEnvDump := parse.MustParseV1TaskRun(
//         t, strings.ReplaceAll(trManifestCompleted, `  - name: Testing|Environment
//     type: string
//     value: |
//       {"step": "date-step", "environment": {
//       "HOME=/root",
//       }},
//       {"step": "time-step", "environment": {
//       "HOME=/froot",
//       }}
//   - name: Testing|FileSystemContent
//     type: string
//     value: '[{"stepName":"/tekton/run/0/status","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"},{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]'
// `, ""),
//     )
//     ttrAbsentTaskTest := parse.MustParseTaskTestRun(t, ttrManifestAbsentTaskTest)
//     ttrAbsentTask := parse.MustParseTaskTestRun(t, ttrManifestAbsentTask)
//     ttrInputsUndeclaredParam := parse.MustParseTaskTestRun(
//         t, fmt.Sprintf(ttrManifestInputsUndeclaredParam, testClock.Now().Format(time.RFC3339)),
//     )
//     ttrInputsUndeclaredEnvStep := parse.MustParseTaskTestRun(
//         t, fmt.Sprintf(ttrManifestInputsUndeclaredStepEnvStep, testClock.Now().Format(time.RFC3339)),
//     )
//     ttrExpectsUndeclaredResult := parse.MustParseTaskTestRun(
//         t, fmt.Sprintf(ttrManifestExpectsUndeclaredResult, testClock.Now().Format(time.RFC3339)),
//     )
//     ttrExpectsUndeclaredEnvStep := parse.MustParseTaskTestRun(
//         t, fmt.Sprintf(ttrManifestExpectsUndeclaredEnvStep, testClock.Now().Format(time.RFC3339)),
//     )
//     ttrExpectsUndeclaredFileSystemStep := parse.MustParseTaskTestRun(
//         t, fmt.Sprintf(
//             ttrManifestExpectsUndeclaredFileSystemStep,
//             testClock.Now().Format(time.RFC3339),
//         ),
//     )
//     ttrCompletedButNoEnvDumpInTR := parse.MustParseTaskTestRun(
//         t, ttrManifestCompletedTaskRunWithTestSpec,
//     )
//     ttrCompletedButNoFileSystemObservationsInTR := ttrCompletedButNoEnvDumpInTR.DeepCopy()
//     ttrCompletedButNoFileSystemObservationsInTR.Name += "-no-expected-env"
//     ttrCompletedButNoFileSystemObservationsInTR.Spec.TaskTestSpec.Expects.Env = nil
//     ttrCompletedButNoFileSystemObservationsInTR.Status.TaskRunName = ptr.To(
//         "ttr-completed-task-run-run",
//     )

//     task := parse.MustParseV1Task(t, tManifest)

//     data := test.Data{
//         Tasks:     []*v1.Task{task},
//         TaskRuns:  []*v1.TaskRun{trCompletedNoEnvDump},
//         TaskTests: []*v1alpha1.TaskTest{},
//         TaskTestRuns: []*v1alpha1.TaskTestRun{ttrAbsentTaskTest, ttrAbsentTask,
//             ttrInputsUndeclaredParam, ttrInputsUndeclaredEnvStep,
//             ttrExpectsUndeclaredResult, ttrExpectsUndeclaredFileSystemStep, ttrExpectsUndeclaredEnvStep,
//             ttrCompletedButNoEnvDumpInTR, ttrCompletedButNoFileSystemObservationsInTR},
//     }

//     type tc struct {
//         ttr                *v1alpha1.TaskTestRun
//         wantErr            error
//         wantTtrStatus      *v1alpha1.TaskTestRunStatus
//         wantCompletionTime bool
//     }
//     tests := map[string]tc{
//         "ttr_references_absent_task_test": {
//             ttr: ttrAbsentTaskTest,
//             wantErr: fmt.Errorf(
//                 "could not prepare reconciliation of task test run invalid-ttr-absent-task-test: %w",
//                 apierrors.NewNotFound(
//                     schema.GroupResource{Group: "tekton.dev", Resource: "tasktests"},
//                     "absent-task-test",
//                 ),
//             ),
//         },
//         "ttr_inputs_result_not_declared_in_task": {
//             ttr: ttrInputsUndeclaredParam,
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{
//                     Conditions: duckv1.Conditions{
//                         {
//                             Type:   "Succeeded",
//                             Status: "False",
//                             Reason: "TaskTestRunValidationFailed",
//                             Message: `validation failed for referenced object: invalid value: foo: status.taskTestSpec.inputs.params[0].name
// task "task" has no Param named "foo"`,
//                         },
//                     },
//                 },
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
//                         Inputs: &v1alpha1.TaskTestInputs{Params: []v1.Param{{
//                             Name:  "foo",
//                             Value: v1.ParamValue{Type: "string", StringVal: "bar"},
//                         }}},
//                     },
//                 },
//             },
//             wantErr: errors.New(
//                 `validation failed for referenced object: invalid value: foo: status.taskTestSpec.inputs.params[0].name
// task "task" has no Param named "foo"`,
//             ),
//             wantCompletionTime: true,
//         },
//         "ttr_inputs_step_for_stepEnv_not_declared_in_task": {
//             ttr: ttrInputsUndeclaredEnvStep,
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{
//                     Conditions: duckv1.Conditions{
//                         {
//                             Type:   "Succeeded",
//                             Status: "False",
//                             Reason: "TaskTestRunValidationFailed",
//                             Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.inputs.stepEnvs[0].stepName
// task "task" has no Step named "goodbye-step"`,
//                         },
//                     },
//                 },
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
//                         Inputs: &v1alpha1.TaskTestInputs{StepEnvs: []v1alpha1.StepEnv{{
//                             StepName: "goodbye-step",
//                             Env: []corev1.EnvVar{{
//                                 Name:  "FOO",
//                                 Value: "BAR",
//                             }},
//                         }}},
//                     },
//                 },
//             },
//             wantErr: errors.New(
//                 `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.inputs.stepEnvs[0].stepName
// task "task" has no Step named "goodbye-step"`,
//             ),
//             wantCompletionTime: true,
//         },
//         "ttr_expects_result_not_declared_in_task": {
//             ttr: ttrExpectsUndeclaredResult,
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{
//                     Conditions: duckv1.Conditions{
//                         {
//                             Type:   "Succeeded",
//                             Status: "False",
//                             Reason: "TaskTestRunValidationFailed",
//                             Message: `validation failed for referenced object: invalid value: current-date: status.taskTestSpec.expected.results[0].name
// task "task" has no Result named "current-date"`,
//                         },
//                     },
//                 },
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
//                         Expects: &v1alpha1.ExpectedOutcomes{
//                             Results: []v1.TaskResult{
//                                 {
//                                     Name:  "current-date",
//                                     Type:  "string",
//                                     Value: &v1.ResultValue{StringVal: "2025-08-15", Type: "string"},
//                                 },
//                                 {
//                                     Name:  "current-time",
//                                     Type:  "string",
//                                     Value: &v1.ResultValue{StringVal: "15:17:59", Type: "string"},
//                                 },
//                             },
//                             SuccessStatus: ptr.To(true),
//                             SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
//                         },
//                     },
//                 },
//             },
//             wantErr: errors.New(
//                 `validation failed for referenced object: invalid value: current-date: status.taskTestSpec.expected.results[0].name
// task "task" has no Result named "current-date"`,
//             ),
//             wantCompletionTime: true,
//         },
//         "ttr_expects_step_for_stepEnv_not_declared_in_task": {
//             ttr: ttrExpectsUndeclaredEnvStep,
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{
//                     Conditions: duckv1.Conditions{
//                         {
//                             Type:   "Succeeded",
//                             Status: "False",
//                             Reason: "TaskTestRunValidationFailed",
//                             Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.stepEnvs[0].stepName
// task "task" has no Step named "goodbye-step"`,
//                         },
//                     },
//                 },
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
//                         Expects: &v1alpha1.ExpectedOutcomes{
//                             StepEnvs: []v1alpha1.StepEnv{{
//                                 StepName: "goodbye-step",
//                                 Env: []corev1.EnvVar{{
//                                     Name:  "HOME",
//                                     Value: "/root",
//                                 }},
//                             }},
//                             SuccessStatus: ptr.To(true),
//                             SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
//                         },
//                     },
//                 },
//             },
//             wantErr: errors.New(
//                 `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.stepEnvs[0].stepName
// task "task" has no Step named "goodbye-step"`,
//             ),
//             wantCompletionTime: true,
//         },
//         "ttr_expects_file_system_step_not_declared_in_task": {
//             ttr: ttrExpectsUndeclaredFileSystemStep,
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{
//                     Conditions: duckv1.Conditions{
//                         {
//                             Type:   "Succeeded",
//                             Status: "False",
//                             Reason: "TaskTestRunValidationFailed",
//                             Message: `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.fileSystemContents[0].stepName
// task "task" has no Step named "goodbye-step"`,
//                         },
//                     },
//                 },
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "task"},
//                         Expects: &v1alpha1.ExpectedOutcomes{
//                             FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
//                                 StepName: "goodbye-step",
//                                 Objects: []v1alpha1.FileSystemObject{{
//                                     Path: "/tekton/results/current-date",
//                                     Type: "Directory",
//                                 }, {
//                                     Path:    "/tekton/results/current-time",
//                                     Type:    "TextFile",
//                                     Content: "foo",
//                                 }},
//                             }},
//                             SuccessStatus: ptr.To(true),
//                             SuccessReason: ptr.To(v1.TaskRunReason("Succeeded")),
//                         },
//                     },
//                 },
//             },
//             wantErr: errors.New(
//                 `validation failed for referenced object: invalid value: goodbye-step: status.taskTestSpec.expected.fileSystemContents[0].stepName
// task "task" has no Step named "goodbye-step"`,
//             ),
//             wantCompletionTime: true,
//         },
//         "tt_references_absent_task": {
//             ttr: ttrAbsentTask,
//             wantErr: fmt.Errorf(
//                 "could not dereference task under test: %w",
//                 apierrors.NewNotFound(
//                     schema.GroupResource{Group: "tekton.dev", Resource: "tasks"},
//                     "absent-task",
//                 ),
//             ),
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{Conditions: duckv1.Conditions{{
//                     Type:   "Succeeded",
//                     Status: "Unknown",
//                     Reason: "Started",
//                 }}},
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "absent-task"},
//                     },
//                 },
//             },
//         },
//         "ttr_expects_env_value_but_no_dump_in_tr": {
//             ttr: ttrCompletedButNoEnvDumpInTR,
//             wantErr: errors.New(
//                 `error occurred while checking expectations: error while checking the expectations for env: could not find environment dump for stepEnv
// error while checking the expectations for file system objects: could not find result with file system observations`,
//             ),
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{Conditions: duckv1.Conditions{{
//                     Type:   "Succeeded",
//                     Status: "False",
//                     Reason: "TaskTestRunValidationFailed",
//                     Message: `error occurred while checking expectations: error while checking the expectations for env: could not find environment dump for stepEnv
// error while checking the expectations for file system objects: could not find result with file system observations`,
//                 }}},
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskRunName: ptr.To("ttr-completed-task-run-run"),
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
//                         Expects: &v1alpha1.ExpectedOutcomes{
//                             Results: []v1.TaskResult{
//                                 {
//                                     Name: "current-date",
//                                     Type: "string",
//                                     Value: &v1.ResultValue{
//                                         StringVal: "2025-08-15",
//                                         Type:      "string",
//                                     },
//                                 },
//                                 {
//                                     Name: "current-time",
//                                     Type: "string",
//                                     Value: &v1.ResultValue{
//                                         StringVal: "15:17:59",
//                                         Type:      "string",
//                                     },
//                                 },
//                             },
//                             Env: []corev1.EnvVar{{
//                                 Name:  "HOME",
//                                 Value: "/root",
//                             }},
//                             FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
//                                 StepName: "date-step",
//                                 Objects: []v1alpha1.FileSystemObject{{
//                                     Path:    "/tekton/results/current-date",
//                                     Type:    "TextFile",
//                                     Content: "bar",
//                                 }},
//                             }},
//                             SuccessStatus: ptr.To(true),
//                             SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
//                         },
//                     },
//                     Outcomes: &v1alpha1.ObservedOutcomes{
//                         Results: &[]v1alpha1.ObservedResults{
//                             {
//                                 Name: "current-date",
//                                 Want: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "2025-08-15",
//                                 },
//                                 Got: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "2025-08-15",
//                                 },
//                             },
//                             {
//                                 Name: "current-time",
//                                 Want: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "15:17:59",
//                                 },
//                                 Got: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "15:17:59",
//                                 },
//                             },
//                         },
//                         SuccessStatus: &v1alpha1.ObservedSuccessStatus{
//                             Want: true,
//                             Got:  true,
//                         },
//                         SuccessReason: &v1alpha1.ObservedSuccessReason{
//                             Want: v1.TaskRunReasonSuccessful,
//                             Got:  v1.TaskRunReasonSuccessful,
//                         },
//                     },
//                 },
//             },
//             wantCompletionTime: true,
//         },
//         "ttr_expects_fs_observations_but_no_observations_in_tr": {
//             ttr: ttrCompletedButNoFileSystemObservationsInTR,
//             wantErr: errors.New(
//                 "error occurred while checking expectations: error while checking the expectations for file system objects: could not find result with file system observations",
//             ),
//             wantTtrStatus: &v1alpha1.TaskTestRunStatus{
//                 Status: duckv1.Status{Conditions: duckv1.Conditions{{
//                     Type:    "Succeeded",
//                     Status:  "False",
//                     Reason:  "TaskTestRunValidationFailed",
//                     Message: "error occurred while checking expectations: error while checking the expectations for file system objects: could not find result with file system observations",
//                 }}},
//                 TaskTestRunStatusFields: v1alpha1.TaskTestRunStatusFields{
//                     TaskRunName: ptr.To("ttr-completed-task-run-run"),
//                     TaskTestSpec: &v1alpha1.TaskTestSpec{
//                         TaskRef: &v1alpha1.SimpleTaskRef{Name: "hello-task"},
//                         Expects: &v1alpha1.ExpectedOutcomes{
//                             Results: []v1.TaskResult{
//                                 {
//                                     Name: "current-date",
//                                     Type: "string",
//                                     Value: &v1.ResultValue{
//                                         StringVal: "2025-08-15",
//                                         Type:      "string",
//                                     },
//                                 },
//                                 {
//                                     Name: "current-time",
//                                     Type: "string",
//                                     Value: &v1.ResultValue{
//                                         StringVal: "15:17:59",
//                                         Type:      "string",
//                                     },
//                                 },
//                             },
//                             FileSystemContents: []v1alpha1.ExpectedStepFileSystemContent{{
//                                 StepName: "date-step",
//                                 Objects: []v1alpha1.FileSystemObject{{
//                                     Path:    "/tekton/results/current-date",
//                                     Type:    "TextFile",
//                                     Content: "bar",
//                                 }},
//                             }},
//                             SuccessStatus: ptr.To(true),
//                             SuccessReason: ptr.To(v1.TaskRunReasonSuccessful),
//                         },
//                     },
//                     Outcomes: &v1alpha1.ObservedOutcomes{
//                         Results: &[]v1alpha1.ObservedResults{
//                             {
//                                 Name: "current-date",
//                                 Want: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "2025-08-15",
//                                 },
//                                 Got: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "2025-08-15",
//                                 },
//                             },
//                             {
//                                 Name: "current-time",
//                                 Want: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "15:17:59",
//                                 },
//                                 Got: &v1.ResultValue{
//                                     Type:      "string",
//                                     StringVal: "15:17:59",
//                                 },
//                             },
//                         },
//                         SuccessStatus: &v1alpha1.ObservedSuccessStatus{
//                             Want: true,
//                             Got:  true,
//                         },
//                         SuccessReason: &v1alpha1.ObservedSuccessReason{
//                             Want: v1.TaskRunReasonSuccessful,
//                             Got:  v1.TaskRunReasonSuccessful,
//                         },
//                     },
//                 },
//             },
//             wantCompletionTime: true,
//         },
//     }
//     for name, tt := range tests {
//         t.Run(name, func(t *testing.T) {
//             testAssets, cancel := getTaskTestRunController(t, data)
//             clients := testAssets.Clients
//             defer cancel()

//             if tt.wantTtrStatus == nil {
//                 tt.wantTtrStatus = &tt.ttr.Status
//             }

//             gotErr := testAssets.Controller.Reconciler.Reconcile(testAssets.Ctx, getRunName(tt.ttr))

//             if gotErr == nil {
//                 if tt.wantErr != nil {
//                     t.Errorf("expected error but got none")
//                 }
//             } else {
//                 if tt.wantErr == nil {
//                     t.Errorf("unexpected error: %v", gotErr)
//                 } else {
//                     if d := cmp.Diff(tt.wantErr.Error(), gotErr.Error()); d != "" {
//                         t.Errorf("Didn't get expected error: %v", diff.PrintWantGot(d))
//                     }
//                 }
//             }

//             ttr, err := clients.Pipeline.TektonV1alpha1().
//                 TaskTestRuns(tt.ttr.Namespace).
//                 Get(testAssets.Ctx, tt.ttr.Name, metav1.GetOptions{})
//             if err != nil {
//                 t.Fatalf("getting updated tasktestrun: %v", err)
//             }

//             if tt.wantCompletionTime && ttr.Status.CompletionTime == nil {
//                 t.Error("TaskTestRun: Didn't expect completion time to be nil")
//             }
//             if !tt.wantCompletionTime && ttr.Status.CompletionTime != nil {
//                 t.Error("TaskTestRun: Expected completion time to be nil")
//             }

//             if d := cmp.Diff(*tt.wantTtrStatus, ttr.Status,
//                 ignoreTaskRunStatus,
//                 ignoreResourceVersion,
//                 ignoreLastTransitionTime,
//                 ignoreStartTimeTaskTestRun,
//                 ignoreCompletionTimeTaskTestRun); d != "" {
//                 t.Errorf("Did not get expected TaskTestRun status: %v", diff.PrintWantGot(d))
//             }
//         })
//     }
// }

// getTaskTestRunController returns an instance of the TaskTestRun controller/reconciler that has been seeded with
// d, where d represents the state of the system (existing resources) needed for the test.
func getTaskTestRunController(t *testing.T, d test.Data) (test.Assets, func()) {
	t.Helper()
	names.TestingSeed()
	return initializeTaskTestRunControllerAssets(t, d, pipeline.Options{Images: pipeline.Images{}})
}

func initializeTaskTestRunControllerAssets(
	t *testing.T,
	d test.Data,
	opts pipeline.Options,
) (test.Assets, func()) {
	t.Helper()
	ctx, _ := ttesting.SetupFakeContext(t)
	loggingConfig := logtesting.TestLogger(t).
		Desugar().
		WithOptions(zap.IncreaseLevel(zap.InfoLevel))
	ctx = logging.WithLogger(ctx, loggingConfig.Sugar())
	ctx = ttesting.SetupFakeCloudClientContext(ctx, d.ExpectedCloudEventCount)
	ctx, cancel := context.WithCancel(ctx)
	test.EnsureConfigurationConfigMapsExist(&d)
	c, informers := test.SeedTestData(t, ctx, d)
	configMapWatcher := cminformer.NewInformedWatcher(c.Kube, system.Namespace())
	ctl := NewController(&opts, testClock)(ctx, configMapWatcher)
	if err := configMapWatcher.Start(ctx.Done()); err != nil {
		t.Fatalf("error starting configmap watcher: %v", err)
	}

	if la, ok := ctl.Reconciler.(pkgreconciler.LeaderAware); ok {
		la.Promote(
			pkgreconciler.UniversalBucket(),
			func(pkgreconciler.Bucket, types.NamespacedName) {},
		)
	}

	return test.Assets{
		Logger:     logging.FromContext(ctx),
		Controller: ctl,
		Clients:    c,
		Informers:  informers,
		Recorder:   controller.GetEventRecorder(ctx).(*record.FakeRecorder),
		Ctx:        ctx,
	}, cancel
}

func getRunName(tr *v1alpha1.TaskTestRun) string {
	return strings.Join([]string{tr.Namespace, tr.Name}, "/")
}

type statusPatchFunc = func(*v1alpha1.TaskTestRun)

func patchTaskTestRun(ttrs *v1alpha1.TaskTestRun, pf statusPatchFunc) *v1alpha1.TaskTestRunStatus {
	ttrsCopy := ttrs.DeepCopy()
	pf(ttrsCopy)
	return &ttrsCopy.Status
}

func generateTaskRun(t *testing.T, yaml, taskTestRunName string, retries ...int) *v1.TaskRun {
	t.Helper()
	if len(retries) == 1 {
		return parse.MustParseV1TaskRun(
			t, fmt.Sprintf(yaml, fmt.Sprintf(taskTestRunName+"-run-%d", retries[0]), taskTestRunName, taskTestRunName))
	}
	return parse.MustParseV1TaskRun(t, fmt.Sprintf(yaml, taskTestRunName+"-run", taskTestRunName, taskTestRunName))
}

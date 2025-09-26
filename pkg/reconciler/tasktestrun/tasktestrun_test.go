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
      #! /bin/env sh

      echo "Hello world!"
      date +%Y-%m-%d | tee $(results.current-date.path)
  - computeResources: {}
    image: alpine
    name: time-step
    script: |
      echo "Hello world!"
      date \+%H:%M:%S | tee $(results.current-time.path)
  volumes:
  - name: copy-volume
    emptyDir: {}
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
    stepExpectations:
    - name: date-step
      fileSystemObjects:
      - path: /tekton/results/current-date
        type: TextFile
        content: bar
    - name: time-step
      fileSystemObjects:
      - path: /tekton/results/current-time
        type: TextFile
        content: bar
      env:
      - name: FHOME
        value: "/froot"
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
const trSpecTemplateExpectedEnv = `
metadata:
  name: %s # TaskTestsRun name
  namespace: foo
  annotations:
  # ExpectedValuesJSON: '{"results":[{"name":"current-date","type":"string","value":"2025-08-15"},{"name":"current-time","type":"string","value":"15:17:59"}],"env":[{"name":"HOME","value":"/root"}],"stepExpectations":[{"name":"date-step","fileSystemObjects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"name":"time-step","fileSystemObjects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}],"env":[{"name":"FHOME","value":"/froot"}]}],"successStatus":true,"successReason":"Succeeded"'
    ExpectedValuesJSON: '{"results":[{"name":"current-date","type":"string","value":"2025-08-15"},{"name":"current-time","type":"string","value":"15:17:59"}],"env":[{"name":"HOME","value":"/root"}],"stepExpectations":[{"name":"date-step","fileSystemObjects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"name":"time-step","fileSystemObjects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}],"env":[{"name":"FHOME","value":"/froot"}]}],"successStatus":true,"successReason":"Succeeded"}'
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
    - name: current-time
      description: |
        The current time in the format hh:mm:ss
      type: string
    - name: current-date
      description: |
        The current date in the format dd:mm:yy
      type: string
    params:
    - name: args
      default: ""
    volumes:
    - name: copy-volume
      emptyDir: {}
    - name: copy-volume-2
      emptyDir: {}
    workspaces:
    - name: hello-workspace
    stepTemplate:
      env:
      - name: FOO
        value: bar
    steps:
    - computeResources: {}
      name: prepare-workspace
      volumeMounts:
      - name: copy-volume
        readOnly: true
        mountPath: /ttf/copyfrom/copy-volume
      - name: copy-volume-2
        readOnly: true
        mountPath: /ttf/copyfrom/copy-volume-2
      image: shell-image
      command: ["sh", "-c"]
      args:
      - |
          mkdir -p $(workspaces.hello-workspace.path)/test
          touch $(workspaces.hello-workspace.path)/test/foo
          printf "%%s" "bar" > $(workspaces.hello-workspace.path)/test/foo
          mkdir -p $(workspaces.hello-workspace.path)/test/dir
          mkdir -p $(workspaces.hello-workspace.path)/test/copy
          cp -R /ttf/copyfrom/copy-volume/data $(workspaces.hello-workspace.path)/test/copy
          mkdir -p $(workspaces.hello-workspace.path)/test
          touch $(workspaces.hello-workspace.path)/test/copy-2
          cat /ttf/copyfrom/copy-volume-2/data > $(workspaces.hello-workspace.path)/test/copy-2
    - computeResources: {}
      env:
      - name: ANOTHER_FOO
        value: ANOTHER_BAR
      image: alpine
      name: date-step
      script: |
        #! /bin/env sh

        envPath="/tekton/results/Testing|Environment"
        echo "The values of all environment variables will be dumped to $envPath before this script exits in order to verify the correct functioning of this step"
        trap 'echo "{\"stepName\": \"date-step\", \"environment\": {
        $(printenv | grep '^HOME=')
        }}," >> "$envPath"' EXIT

        echo "Hello world!"
        date +%%Y-%%m-%%d | tee $(results.current-date.path)
    - computeResources: {}
      image: alpine
      name: time-step
      script: |
        envPath="/tekton/results/Testing|Environment"
        echo "The values of all environment variables will be dumped to $envPath before this script exits in order to verify the correct functioning of this step"
        trap 'echo "{\"stepName\": \"time-step\", \"environment\": {
        $(printenv | grep '^HOME=\|^FHOME=')
        }}," >> "$envPath"' EXIT

        echo "Hello world!"
        date \+%%H:%%M:%%S | tee $(results.current-time.path)
  params:
  - name: "args"
    value: "arg"`

const trSpecTemplateNoExpectedEnv = `
metadata:
  name: %s # TaskTestsRun name
  namespace: foo
  annotations:
    ExpectedValuesJSON: '{"results":[{"name":"current-date","type":"string","value":"2025-08-15"},{"name":"current-time","type":"string","value":"15:17:59"}],"stepExpectations":[{"name":"date-step","fileSystemObjects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"}]},{"name":"time-step","fileSystemObjects":[{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}],"env":[{"name":"FHOME","value":"/froot"}]}],"successStatus":true,"successReason":"Succeeded"}'
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
    - name: current-time
      description: |
        The current time in the format hh:mm:ss
      type: string
    - name: current-date
      description: |
        The current date in the format dd:mm:yy
      type: string
    params:
    - name: args
      default: ""
    volumes:
    - name: copy-volume
      emptyDir: {}
    - name: copy-volume-2
      emptyDir: {}
    workspaces:
    - name: hello-workspace
    stepTemplate:
      env:
      - name: FOO
        value: bar
    steps:
    - computeResources: {}
      name: prepare-workspace
      volumeMounts:
      - name: copy-volume
        readOnly: true
        mountPath: /ttf/copyfrom/copy-volume
      - name: copy-volume-2
        readOnly: true
        mountPath: /ttf/copyfrom/copy-volume-2
      image: shell-image
      command: ["sh", "-c"]
      args:
      - |
          mkdir -p $(workspaces.hello-workspace.path)/test
          touch $(workspaces.hello-workspace.path)/test/foo
          printf "%%s" "bar" > $(workspaces.hello-workspace.path)/test/foo
          mkdir -p $(workspaces.hello-workspace.path)/test/dir
          mkdir -p $(workspaces.hello-workspace.path)/test/copy
          cp -R /ttf/copyfrom/copy-volume/data $(workspaces.hello-workspace.path)/test/copy
          mkdir -p $(workspaces.hello-workspace.path)/test
          touch $(workspaces.hello-workspace.path)/test/copy-2
          cat /ttf/copyfrom/copy-volume-2/data > $(workspaces.hello-workspace.path)/test/copy-2
    - computeResources: {}
      env:
      - name: ANOTHER_FOO
        value: ANOTHER_BAR
      image: alpine
      name: date-step
      script: |
        #! /bin/env sh

        echo "Hello world!"
        date +%%Y-%%m-%%d | tee $(results.current-date.path)
    - computeResources: {}
      image: alpine
      name: time-step
      script: |
        envPath="/tekton/results/Testing|Environment"
        echo "The values of all environment variables will be dumped to $envPath before this script exits in order to verify the correct functioning of this step"
        trap 'echo "{\"stepName\": \"time-step\", \"environment\": {
        $(printenv | grep '^FHOME=')
        }}," >> "$envPath"' EXIT

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

const trSpecCancelled = `
  status: TaskRunCancelled
  statusMessage: %s
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
      {"stepName": "date-step", "environment": {
      HOME=/root
      }},
      {"stepName": "time-step", "environment": {
      FHOME=/froot
      HOME=/root
      }},
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
        #! /bin/env sh

        echo "Hello world!"
        date +%%Y-%%m-%%d | tee /tekton/results/current-date
    - computeResources: {}
      image: alpine
      name: time-step
      script: |
        echo "Hello world!"
        date \+%%H:%%M:%%S | tee /tekton/results/current-time
`

const trStatusFinishedNoEnv = `
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
      {"stepName": "time-step", "environment": {
      FHOME=/froot
      }},
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
        #! /bin/env sh

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
var ttrSpecTemplateDecTestExpectedEnv = strings.Replace(ttrSpecTemplateDecTestNoExpectedEnv, "stepExpectations:", `env:
      - name: HOME
        value: "/root"
      stepExpectations:`, 1)

const ttrSpecTemplateDecTestNoExpectedEnv = `
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
        - path: /test/copy
          type: Directory
          content:
            copyFrom:
              volumeName: copy-volume
              path: /data
        - path: /test/copy-2
          type: TextFile
          content:
            copyFrom:
              volumeName: copy-volume-2
              path: /data
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
      stepExpectations:
      - name: date-step
        fileSystemObjects:
        - path: /tekton/results/current-date
          type: TextFile
          content: bar
      - name: time-step
        fileSystemObjects:
        - path: /tekton/results/current-time
          type: TextFile
          content: bar
        env:
        - name: FHOME
          value: "/froot"
  volumes:
    - name: copy-volume-2
      emptyDir: {}
  retries: %s
  allTriesMustSucceed: %s
  status: %s
`

const ttrSpecTemplateDecTestInaccurateExpectations = `
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
      stepExpectations:
      - name: date-step
        fileSystemObjects:
        - path: /tekton/results/current-date
          type: Directory
      - name: time-step
        fileSystemObjects:
        - path: /tekton/results/current-time
          type: TextFile
          content: foo
        env:
        - name: FHOME
          value: "/froot"
  retries: %s
  allTriesMustSucceed: %s
  status: %s
`

const ttrSpecTemplateRefTest = `
metadata:
  name: %s
  namespace: foo
spec:
  taskTestRef:
    name: task-test
  retries: 1
`

const ttrStatusRunning = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  conditions:
  - type: Succeeded
    reason: Started
    status: Unknown
  taskRunName : %s
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
      status: "%s"
      reason: %s
    taskRunName: %s
`

const ttrStatusCompletedSuccessful = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  completionTime: "2025-08-15T15:17:59Z"
  conditions:
  - type: Succeeded
    status: "True"
    reason: Succeeded
    message: TaskRun completed executing and outcomes were as expected
  taskRunName: %s
`

const ttrStatusCompletedFailed = `
status:
  startTime:  "2025-08-15T15:17:55Z"
  completionTime: "2025-08-15T15:17:59Z"
  conditions:
  - type: Succeeded
    status: "False"
    reason: TaskTestRunUnexpectedOutcomes
    message: not all expectations were met
  taskRunName : %s
`

func TestReconciler_ValidateReconcileKind(t *testing.T) {
	const (
		tcStartNewRunDecTest                             = "start-new-run-dec-test"
		tcCheckRunningDecTest                            = "check-running-dec-test"
		tcCancelRunningDecTest                           = "cancel-running-dec-test"
		tcCancelTimeoutDecTest                           = "cancel-timeout-dec-test"
		tcCheckCompletedSuccessfulDecTest                = "check-completed-successful-dec-test"
		tcCheckCompletedSuccessfulDecTestNoEnv           = "check-completed-successful-dec-test-no-env"
		tcCheckCompletedSuccessfulRefTest                = "check-completed-successful-referenced-test"
		tcCheckCompletedSuccessDecTestRetriesMustSucceed = "check-completed-successful-dec-test-with-retries"
		tcCheckCompletedFailedDecTestNoRetries           = "check-completed-failed-dec-test-no-retries"
		tcCheckCompletedFailedDecTestRetries             = "check-completed-failed-dec-test-with-retries"
		tcCheckCompletedFailedDecTestRetriesMustSucceed  = "check-completed-failed-dec-test-retries-must-succeed"
		tcStartRetryFailedDecTest                        = "start-retry-failed-dec-test"
		tcStartRetrySuccessfulDecTest                    = "start-retry-successful-dec-test"
	)

	// fill maps
	taskRunMap := map[string]*v1.TaskRun{
		tcCheckRunningDecTest:                           generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcCheckRunningDecTest),
		tcCancelRunningDecTest:                          generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcCancelRunningDecTest),
		tcCancelTimeoutDecTest:                          generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcCancelTimeoutDecTest),
		tcCheckCompletedSuccessfulDecTest:               generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcCheckCompletedSuccessfulDecTest),
		tcCheckCompletedSuccessfulDecTestNoEnv:          generateTaskRun(t, trSpecTemplateNoExpectedEnv+trStatusFinishedNoEnv, tcCheckCompletedSuccessfulDecTestNoEnv),
		tcCheckCompletedSuccessfulRefTest:               generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcCheckCompletedSuccessfulRefTest),
		tcCheckCompletedFailedDecTestNoRetries:          generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcCheckCompletedFailedDecTestNoRetries),
		tcCheckCompletedFailedDecTestRetries:            generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcCheckCompletedFailedDecTestRetries, 0),
		tcCheckCompletedFailedDecTestRetriesMustSucceed: generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcCheckCompletedFailedDecTestRetriesMustSucceed, 0),
		tcStartRetryFailedDecTest:                       generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcStartRetryFailedDecTest, 0),
		tcStartRetrySuccessfulDecTest:                   generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusFinished, tcStartRetrySuccessfulDecTest, 0),
	}

	taskTestMap := map[string]*v1alpha1.TaskTest{
		tcCheckCompletedSuccessfulRefTest: parse.MustParseTaskTest(t, ttManifest),
	}

	taskTestRunMap := map[string]*v1alpha1.TaskTestRun{
		tcStartNewRunDecTest:                             generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv, tcStartNewRunDecTest),
		tcCheckRunningDecTest:                            generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusRunning, tcCheckRunningDecTest+"-run"), tcCheckRunningDecTest),
		tcCancelRunningDecTest:                           generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusRunning, tcCancelRunningDecTest+"-run"), tcCancelRunningDecTest, "0", "false", "TaskTestRunCancelled"),
		tcCancelTimeoutDecTest:                           generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusRunning, tcCancelTimeoutDecTest+"-run"), tcCancelTimeoutDecTest),
		tcCheckCompletedSuccessfulDecTest:                generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+"\nstatus:\n  taskRunName: "+tcCheckCompletedSuccessfulDecTest+"-run", tcCheckCompletedSuccessfulDecTest),
		tcCheckCompletedSuccessfulDecTestNoEnv:           generateTaskTestRun(t, ttrSpecTemplateDecTestNoExpectedEnv+"\nstatus:\n  taskRunName: "+tcCheckCompletedSuccessfulDecTestNoEnv+"-run", tcCheckCompletedSuccessfulDecTestNoEnv),
		tcCheckCompletedSuccessfulRefTest:                parse.MustParseTaskTestRun(t, fmt.Sprintf(ttrSpecTemplateRefTest, tcCheckCompletedSuccessfulRefTest)),
		tcCheckCompletedSuccessDecTestRetriesMustSucceed: generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusCompletedSuccessful, tcCheckCompletedSuccessDecTestRetriesMustSucceed+"-run-0"), tcCheckCompletedSuccessDecTestRetriesMustSucceed, "1", "true"),
		tcCheckCompletedFailedDecTestNoRetries:           generateTaskTestRun(t, ttrSpecTemplateDecTestInaccurateExpectations, tcCheckCompletedFailedDecTestNoRetries),
		tcCheckCompletedFailedDecTestRetries:             generateTaskTestRun(t, ttrSpecTemplateDecTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedDecTestRetries+"-run-0"), tcCheckCompletedFailedDecTestRetries, "1"),
		tcCheckCompletedFailedDecTestRetriesMustSucceed: patchTaskTestRun(generateTaskTestRun(t, ttrSpecTemplateDecTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedDecTestRetriesMustSucceed+"-run-0"), tcCheckCompletedFailedDecTestRetriesMustSucceed, "1", "true"), func(ttr *v1alpha1.TaskTestRun) {
			ttr.Status.TaskRunStatus = &taskRunMap[tcCheckCompletedFailedDecTestRetriesMustSucceed].Status
		}),
		tcStartRetryFailedDecTest:     generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusToBeRetried, "False", "TaskTestRunUnexpectedOutcomes", tcStartRetryFailedDecTest+"-run-0"), tcStartRetryFailedDecTest, "1"),
		tcStartRetrySuccessfulDecTest: generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusToBeRetried, "True", "Succeeded", tcStartRetrySuccessfulDecTest+"-run-0"), tcStartRetrySuccessfulDecTest, "1", "true"),
	}

	taskTestRunMap[tcCancelRunningDecTest].Status.TaskRunStatus = &taskRunMap[tcCancelRunningDecTest].Status

	taskTestRunMap[tcCancelTimeoutDecTest].Status.TaskRunStatus = &taskRunMap[tcCancelRunningDecTest].Status
	taskTestRunMap[tcCancelTimeoutDecTest].Status.StartTime = &metav1.Time{Time: time.Date(1922, time.January, 1, 0, 0, 0, 0, time.UTC)}

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

	type tc struct {
		ttr                   *v1alpha1.TaskTestRun
		wantTtrStatus         *v1alpha1.TaskTestRunStatus
		wantTr                *v1.TaskRun
		wantStartTime         bool
		wantTtrCompletionTime bool
		wantTrCompletionTime  *bool
	}
	tests := map[string]tc{
		tcStartNewRunDecTest: {
			ttr: taskTestRunMap[tcStartNewRunDecTest],
			wantTtrStatus: patchTaskTestRunStatus(generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusRunning, tcStartNewRunDecTest+"-run"), tcStartNewRunDecTest), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcStartNewRunDecTest),
			wantStartTime: true,
		},
		tcCheckRunningDecTest: {
			ttr: taskTestRunMap[tcCheckRunningDecTest],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcCheckRunningDecTest],
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskRunName = ptr.To(tcCheckRunningDecTest + "-run")
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
				}),
			wantTr:        taskRunMap[tcCheckRunningDecTest],
			wantStartTime: true,
		},
		tcCancelRunningDecTest: {
			ttr: taskTestRunMap[tcCancelRunningDecTest],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcCancelRunningDecTest], func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "False",
					Reason:  "TaskTestRunCancelled",
					Message: `TaskTestRun "` + tcCancelRunningDecTest + `" was cancelled. `,
				}}
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:                generateTaskRun(t, trSpecTemplateExpectedEnv+fmt.Sprintf(trSpecCancelled, "TaskRun cancelled as the TaskTestRun it belongs to has been cancelled.")+trStatusRunning, tcCancelRunningDecTest),
			wantStartTime:         true,
			wantTtrCompletionTime: true,
			wantTrCompletionTime:  ptr.To(false),
		},
		tcCancelTimeoutDecTest: {
			ttr: taskTestRunMap[tcCancelTimeoutDecTest],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcCancelTimeoutDecTest], func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:    "Succeeded",
					Status:  "False",
					Reason:  "TaskTestRunTimedOut",
					Message: `TaskTestRun "` + tcCancelTimeoutDecTest + `" failed to finish within "1h0m0s"`,
				}}
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:                generateTaskRun(t, trSpecTemplateExpectedEnv+fmt.Sprintf(trSpecCancelled, "TaskRun cancelled as the TaskTestRun it belongs to has timed out.")+trStatusRunning, tcCancelTimeoutDecTest),
			wantStartTime:         true,
			wantTtrCompletionTime: true,
			wantTrCompletionTime:  ptr.To(false),
		},
		tcCheckCompletedSuccessfulDecTest: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessfulDecTest],
			wantTtrStatus: patchTaskTestRunStatus(generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusCompletedSuccessful, tcCheckCompletedSuccessfulDecTest+"-run"), tcCheckCompletedSuccessfulDecTest),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec

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
			wantTr:                taskRunMap[tcCheckCompletedSuccessfulDecTest],
			wantStartTime:         true,
			wantTtrCompletionTime: true,
		},
		tcCheckCompletedSuccessfulDecTestNoEnv: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessfulDecTestNoEnv],
			wantTtrStatus: patchTaskTestRunStatus(generateTaskTestRun(t, ttrSpecTemplateDecTestNoExpectedEnv+fmt.Sprintf(ttrStatusCompletedSuccessful, tcCheckCompletedSuccessfulDecTestNoEnv+"-run"), tcCheckCompletedSuccessfulDecTestNoEnv),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec

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
							StepName: "time-step",
							Env: []v1alpha1.ObservedEnvVar{{
								Name: "FHOME",
								Want: "/froot",
								Got:  "/froot",
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
			wantTr:                taskRunMap[tcCheckCompletedSuccessfulDecTestNoEnv],
			wantStartTime:         true,
			wantTtrCompletionTime: true,
		},
		tcCheckCompletedSuccessfulRefTest: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessfulRefTest],
			wantTtrStatus: patchTaskTestRunStatus(
				generateTaskTestRun(t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusCompletedSuccessful, tcCheckCompletedSuccessfulRefTest+"-run"), tcCheckCompletedSuccessfulRefTest),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.Conditions = duckv1.Conditions{{
						Type:    "Succeeded",
						Status:  "True",
						Reason:  "Succeeded",
						Message: "TaskRun completed executing and outcomes were as expected",
					}}
					ttr.Status.TaskTestName = ptr.To("task-test")
					ttr.Status.TaskTestSpec = &taskTestMap[tcCheckCompletedSuccessfulRefTest].Spec
					ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{
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
					}
				},
			),
			wantTr:                taskRunMap[tcCheckCompletedSuccessfulRefTest],
			wantStartTime:         true,
			wantTtrCompletionTime: true,
		},
		tcCheckCompletedFailedDecTestNoRetries: {
			ttr: taskTestRunMap[tcCheckCompletedFailedDecTestNoRetries],
			wantTtrStatus: patchTaskTestRunStatus(
				generateTaskTestRun(t, ttrSpecTemplateDecTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedDecTestNoRetries+"-run"), tcCheckCompletedFailedDecTestNoRetries),
				func(ttr *v1alpha1.TaskTestRun) {
					ttr.Status.Outcomes = &v1alpha1.ObservedOutcomes{}
					ttr.Status.TaskTestRunStatusFields = v1alpha1.TaskTestRunStatusFields{
						TaskRunName: ptr.To(tcCheckCompletedFailedDecTestNoRetries + "-run"),
						Outcomes: &v1alpha1.ObservedOutcomes{
							Results: &[]v1alpha1.ObservedResults{{
								Name: "current-date",
								Want: &v1.ResultValue{Type: "string", StringVal: "2015-08-15"},
								Got:  &v1.ResultValue{Type: "string", StringVal: "2025-08-15"},
							}, {
								Name: "current-time",
								Want: &v1.ResultValue{Type: "string", StringVal: "05:17:59"},
								Got:  &v1.ResultValue{Type: "string", StringVal: "15:17:59"},
							}},
							StepEnvs: &[]v1alpha1.ObservedStepEnv{{
								StepName: "date-step",
								Env: []v1alpha1.ObservedEnvVar{{
									Name: "HOME",
									Want: "/groot",
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
									Want: "/groot",
									Got:  "/root",
								}},
							}},
							FileSystemObjects: &[]v1alpha1.ObservedStepFileSystemContent{{
								StepName: "date-step",
								Objects: []v1alpha1.ObservedFileSystemObject{{
									Path:       "/tekton/results/current-date",
									WantType:   v1alpha1.DirectoryType,
									GotType:    v1alpha1.TextFileType,
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
								}},
							}},
							SuccessStatus: &v1alpha1.ObservedSuccessStatus{
								Want: false,
								Got:  true,
							},
							SuccessReason: &v1alpha1.ObservedSuccessReason{
								Want: "Failed",
								Got:  "Succeeded",
							},
							Diffs: `observed success status did not match expectation
observed success reason did not match expectation
Result "current-date": want "2015-08-15", got "2025-08-15"
Result "current-time": want "05:17:59", got "15:17:59"
file system object "/tekton/results/current-date" type in step "date-step": want "Directory", got "TextFile"
file system object "/tekton/results/current-time" content in step "time-step": want "foo", got "bar"
envVar "HOME" in step "date-step": want "/groot", got "/root"
envVar "HOME" in step "time-step": want "/groot", got "/root"
`,
						},
					}
					ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
				},
			),
			wantTr:                taskRunMap[tcCheckCompletedFailedDecTestNoRetries],
			wantStartTime:         true,
			wantTtrCompletionTime: true,
		},
		tcCheckCompletedFailedDecTestRetries: {
			ttr: taskTestRunMap[tcCheckCompletedFailedDecTestRetries],
			wantTtrStatus: patchTaskTestRunStatus(
				generateTaskTestRun(t, ttrSpecTemplateDecTestInaccurateExpectations+fmt.Sprintf(ttrStatusCompletedFailed, tcCheckCompletedFailedDecTestRetries+"-run-0"), tcCheckCompletedFailedDecTestRetries),
				func(ttr *v1alpha1.TaskTestRun) {
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
		tcCheckCompletedFailedDecTestRetriesMustSucceed: {
			ttr: taskTestRunMap[tcCheckCompletedFailedDecTestRetriesMustSucceed],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcCheckCompletedFailedDecTestRetriesMustSucceed].DeepCopy(), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:                taskRunMap[tcCheckCompletedFailedDecTestRetriesMustSucceed],
			wantStartTime:         true,
			wantTtrCompletionTime: true,
		},
		tcStartRetryFailedDecTest: {
			ttr: taskTestRunMap[tcStartRetryFailedDecTest],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcStartRetryFailedDecTest].DeepCopy(), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttr.Status.TaskRunName = ptr.To(tcStartRetryFailedDecTest + "-run-1")
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcStartRetryFailedDecTest, 1),
			wantStartTime: true,
		},
		tcCheckCompletedSuccessDecTestRetriesMustSucceed: {
			ttr: taskTestRunMap[tcCheckCompletedSuccessDecTestRetriesMustSucceed],
			wantTtrStatus: patchTaskTestRunStatus(
				generateTaskTestRun(
					t, ttrSpecTemplateDecTestExpectedEnv+fmt.Sprintf(ttrStatusCompletedSuccessful, tcCheckCompletedSuccessDecTestRetriesMustSucceed+"-run-0"), tcCheckCompletedSuccessDecTestRetriesMustSucceed), func(ttr *v1alpha1.TaskTestRun) {
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
						Message: "TaskRun completed executing and outcomes were as expected",
					}}
				}),
		},
		tcStartRetrySuccessfulDecTest: {
			ttr: taskTestRunMap[tcStartRetrySuccessfulDecTest],
			wantTtrStatus: patchTaskTestRunStatus(taskTestRunMap[tcStartRetrySuccessfulDecTest].DeepCopy(), func(ttr *v1alpha1.TaskTestRun) {
				ttr.Status.Conditions = duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "Unknown",
					Reason: "Started",
				}}
				ttr.Status.TaskRunName = ptr.To(tcStartRetrySuccessfulDecTest + "-run-1")
				ttr.Status.TaskTestSpec = ttr.Spec.TaskTestSpec
			}),
			wantTr:        generateTaskRun(t, trSpecTemplateExpectedEnv+trStatusRunning, tcStartRetrySuccessfulDecTest, 1),
			wantStartTime: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testAssets, cancel := getTaskTestRunController(t, data)
			clients := testAssets.Clients
			defer cancel()

			if tt.wantTrCompletionTime == nil {
				tt.wantTrCompletionTime = &tt.wantTtrCompletionTime
			}

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
			if tt.wantTtrCompletionTime && ttr.Status.CompletionTime == nil {
				t.Error("TaskTestRun: Didn't expect completion time to be nil")
			}
			if !tt.wantTtrCompletionTime && ttr.Status.CompletionTime != nil {
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
				if *tt.wantTrCompletionTime && tr.Status.CompletionTime == nil {
					t.Error("TaskRun: Didn't expect completion time to be nil")
				}
				if !*tt.wantTrCompletionTime && tr.Status.CompletionTime != nil {
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
	return initializeTaskTestRunControllerAssets(t, d, pipeline.Options{Images: pipeline.Images{ShellImage: "shell-image"}})
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

func patchTaskTestRunStatus(ttrs *v1alpha1.TaskTestRun, pf statusPatchFunc) *v1alpha1.TaskTestRunStatus {
	return &(patchTaskTestRun(ttrs, pf).Status)
}

func patchTaskTestRun(ttrs *v1alpha1.TaskTestRun, pf statusPatchFunc) *v1alpha1.TaskTestRun {
	ttrsCopy := ttrs.DeepCopy()
	pf(ttrsCopy)
	return ttrsCopy
}

func generateTaskRun(t *testing.T, yaml, taskTestRunName string, retries ...int) *v1.TaskRun {
	t.Helper()
	if len(retries) == 1 {
		return parse.MustParseV1TaskRun(
			t, fmt.Sprintf(yaml, fmt.Sprintf(taskTestRunName+"-run-%d", retries[0]), taskTestRunName, taskTestRunName))
	}
	return parse.MustParseV1TaskRun(t, fmt.Sprintf(yaml, taskTestRunName+"-run", taskTestRunName, taskTestRunName))
}

func generateTaskTestRun(t *testing.T, yaml, taskTestRunName string, optionalArgs ...string) *v1alpha1.TaskTestRun {
	t.Helper()

	if len(optionalArgs) > 3 {
		panic("only three optional args allowed for this function")
	}
	if len(optionalArgs) < 3 {
		optionalArgs = append(optionalArgs, "", "", "")
	}

	if optionalArgs[0] == "" {
		optionalArgs[0] = "0"
	}

	if optionalArgs[1] == "" {
		optionalArgs[1] = "false"
	}

	if optionalArgs[2] == "" {
		optionalArgs[2] = `""`
	}

	return parse.MustParseTaskTestRun(t, fmt.Sprintf(yaml, taskTestRunName, optionalArgs[0], optionalArgs[1], optionalArgs[2]))
}

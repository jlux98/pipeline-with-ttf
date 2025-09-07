package tasktestsuiterun

// Task manifests
const tManifest = `
metadata:
  name: task
  namespace: foo
spec:
  steps:
  - command:
    - /mycmd
    env:
    - name: foo
      value: bar
    image: foo
    name: simple-step`

// TaskTest manifests
const ttManifest = `
metadata:
  name: task-test
  namespace: foo
spec:
  taskRef:
    name: task
  expects:
    successStatus: true
    successReason: Succeeded
`

// TaskTestSuite manifests
const ttsManifestSimpleSuite = `
metadata:
  name: suite
  namespace: foo
spec:
  taskTests:
  - name: task-0
    taskTestRef:
      name: task-test
  - name: task-1
    taskTestSpec:
      taskRef:
        name: task
      expects:
        successStatus: true
        successReason: Succeeded
`

const ttsManifestSimpleSuiteOnErrorContinue = `
metadata:
  name: suite-onerror-continue
  namespace: foo
spec:
  taskTests:
  - name: task-0
    taskTestRef:
      name: task-test
  - name: task-1
    taskTestSpec:
      taskRef:
        name: task
      expects:
        successStatus: true
        successReason: Succeeded
    onError: Continue
`

// Valid TaskTestRun manifests
const ttrManifestTemplateSpec = `
metadata:
  name: %s-%s
  namespace: foo
  labels:
    tekton.dev/taskTestSuiteRun: %s
    tekton.dev/suiteTest: %s
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestSuiteRun
    name: %s
    controller: true
    blockOwnerDeletion: true
spec:
  retries: %s
  allTriesMustSucceed: %s
  workspaces:
  - name: time-workspace
    emptyDir: {}
  volumes:
  - name: copy-volume
    emptyDir: {}
  taskTestSpec:
    taskRef:
      name: task
    expects:
      successStatus: true
      successReason: Succeeded
`

const ttrSpecCancelled = `
  status: TaskTestRunCancelled
  statusMessage: %s
`

const ttrStatusRunning = `
status:
  conditions:
  - type: Succeeded
    status: Unknown
    reason: Started
  startTime: "2025-08-15T15:17:55Z"
`

const ttrTemplateCompletedSuccess = ttrManifestTemplateSpec + ttrStatusCompletedSuccess

const ttrStatusCompletedSuccess = `
status:
  conditions:
  - type: Succeeded
    status: "True"
    reason: Succeeded
    message: TaskRun completed executing and outcomes were as expected
  taskTestSpec:
    taskRef:
      name: task
    expects:
      successStatus: true
      successReason: Succeeded
  taskRunName: ttsr-check-completed-runs-inline-tts-task-0-run
  taskRunStatus:
    completionTime: "2025-08-15T15:17:59Z"
    conditions:
    - message: All Steps have completed executing
      reason: Succeeded
      status: "True"
      type: Succeeded
    podName: ttsr-check-completed-runs-inline-tts-task-0-run-abcde-pod
    startTime: "2025-08-15T15:17:55Z"
    taskSpec:
      steps:
      - command:
        - /mycmd
        env:
        - name: foo
          value: bar
        image: foo
        name: simple-step
  outcomes:
    successStatus:
      want: true
      got: true
    successReason:
      want: Succeeded
      got: Succeeded
  startTime: "2025-08-15T15:17:55Z"
  completionTime: "2025-08-15T15:17:59Z"
`

const ttrTemplateCompletedFail = ttrManifestTemplateSpec + ttrStatusCompletedFail

const ttrStatusCompletedFail = `
status:
  conditions:
  - type: Succeeded
    status: "False"
    reason: TaskTestRunUnexpectedOutcomes
    message: |
      not all expectations were met:
      observed success status did not match expectation
      observed success reason did not match expectation
  taskTestSpec:
    taskRef:
      name: task
    expects:
      successStatus: false
      successReason: BOOM
  taskRunName: ttsr-check-completed-runs-inline-tts-task-0-run
  taskRunStatus:
    completionTime: "2025-08-15T15:17:59Z"
    conditions:
    - message: All Steps have completed executing
      reason: Succeeded
      status: "True"
      type: Succeeded
    podName: ttsr-check-completed-runs-inline-tts-task-0-run-abcde-pod
    startTime: "2025-08-15T15:17:55Z"
    taskSpec:
      steps:
      - command:
        - /mycmd
        env:
        - name: foo
          value: bar
        image: foo
        name: simple-step
  outcomes:
    successStatus:
      want: false
      got: true
    successReason:
      want: BOOM
      got: Succeeded
  startTime: "2025-08-15T15:17:55Z"
  completionTime: "2025-08-15T15:17:59Z"
`

// Valid TaskTestSuiteRun manifests
const ttsrManifestTemplateInlineTts = `
metadata:
  name: %s
  namespace: foo
spec:
  executionMode: %s
  defaultRunSpecTemplate:
    volumes:
    - name: copy-volume
      emptyDir: {}
    workspaces:
    - name: time-workspace
      emptyDir: {}
  taskTestSuiteSpec:
    taskTests:
    - name: task-0
      retries: %s
      allTriesMustSucceed: %s
      taskTestRef:
        name: task-test
    - name: task-1
      taskTestSpec:
        taskRef:
          name: task
        expects:
          successStatus: true
          successReason: Succeeded
      retries: %s
      allTriesMustSucceed: %s
  runSpecs:
  - name: task-1
    workspaces:
    - name: date-workspace
      emptyDir: {}
    volumes:
    - name: copy-volume-2
      emptyDir: {}
`

const ttsrManifestTemplateReferencedTts = `
metadata:
  name: %s
  namespace: foo
spec:
  executionMode: Parallel
  defaultRunSpecTemplate:
    workspaces:
    - name: time-workspace
      emptyDir: {}
    volumes:
    - name: copy-volume
      emptyDir: {}
  taskTestSuiteRef:
    name: suite
  runSpecs:
  - name: task-1
    workspaces:
    - name: date-workspace
      emptyDir: {}
    volumes:
    - name: copy-volume-2
      emptyDir: {}
`

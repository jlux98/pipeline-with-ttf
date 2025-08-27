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

const tManifestHelloTask = `
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
  steps:
  - computeResources: {}
    image: alpine
    name: hello-step
    script: |
      echo "Hello world!"
      date +%H:%M:%S | tee $(results.current-time.path)
      date +%Y-%m-%d | tee $(results.current-date.path)`

// TaskTest manifests
const ttManifest = `
metadata:
  name: task-test
  namespace: foo
spec:
  taskRef:
    name: task
`

// TaskRun manifests
const trManifestJustStarted = `
metadata:
  name: ttr-newly-created-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-newly-created
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-newly-created
    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: task
status:
  conditions:
    - reason: Started
      status: Unknown
      type: Succeeded`

const trManifestAlreadyStarted = `
metadata:
  name: task-test-run123-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-existing-taskrun

  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-existing-taskrun

    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: task
status:
  conditions:
    - reason: Started
      status: Unknown
      type: Succeeded
  startTime:  "2025-08-15T15:17:55Z"`

const trManifestCompleted = `
metadata:
  name: ttr-completed-task-run-run
  namespace: foo
  labels:
    tekton.dev/taskTestRun: ttr-completed-task-run
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestRun
    name: ttr-completed-task-run
    controller: true
    blockOwnerDeletion: true
spec:
  taskRef:
    name: hello-task
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
      {"step": "hello-step", "environment": {
      "HOME=/root",
      }}
  - name: Testing|FileSystemContent
    type: string
    value: '[{"stepName":"/tekton/run/0/status","objects":[{"path":"/tekton/results/current-date","type":"TextFile","content":"bar"},{"path":"/tekton/results/current-time","type":"TextFile","content":"bar"}]}]'
  startTime: "2025-08-15T15:17:55Z"
  steps:
  - container: step-hello-step
    imageID: docker.io/library/alpine@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1
    name: hello-step
    terminated:
      containerID: containerd://13d2449b5cf780bbcdd7e7fd489f72beb69d072eba12bbbf5de77b06098b5b48
      exitCode: 0
      finishedAt: "2025-08-15T15:17:59Z"
      message: '[{"key":"current-date","value":"2025-08-15\n","type":1},{"key":"current-time","value":"15:17:59\n","type":1}]'
      reason: Completed
      startedAt: "2025-08-15T15:17:59Z"
    terminationReason: Completed
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
    steps:
    - computeResources: {}
      image: alpine
      name: hello-step
      script: |
        echo "Hello world!"
        date +%H:%M:%S | tee /tekton/results/current-time
        date +%Y-%m-%d | tee /tekton/results/current-date`

// Valid TaskTestRun manifests
const ttrManifestSimpleInlineTtsTask0 = `
metadata:
  name: simple-ttsr-inline-tts-task-0
  namespace: foo
  labels:
    tekton.dev/taskTestSuiteRun: simple-ttsr-inline-tts
    tekton.dev/suiteTest: task-0
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestSuiteRun
    name: simple-ttsr-inline-tts
    controller: true
    blockOwnerDeletion: true
spec:
  taskTestSpec:
    taskRef:
      name: task
status:
  conditions:
  - type: Succeeded
    status: Unknown
    reason: Started
`

const ttrManifestSimpleInlineTtsTask1 = `
metadata:
  name: simple-ttsr-inline-tts-task-1
  namespace: foo
  labels:
    tekton.dev/taskTestSuiteRun: simple-ttsr-inline-tts
    tekton.dev/suiteTest: task-1
  ownerReferences:
  - apiVersion: tekton.dev/v1alpha1
    kind: TaskTestSuiteRun
    name: simple-ttsr-inline-tts
    controller: true
    blockOwnerDeletion: true
spec:
  taskTestSpec:
    taskRef:
      name: task
status:
  conditions:
  - type: Succeeded
    status: Unknown
    reason: Started
`

// Invalid TaskTestRun manifests
const ttrManifestAbsentTaskTest = `
metadata:
  name: invalid-ttr-absent-task-test
  namespace: foo
spec:
  taskTestRef:
    name: absent-task-test`

const ttrManifestExpectsUndeclaredResult = `
metadata:
  name: invalid-ttr-expects-undeclared-result
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expected:
      results:
      - name: current-date
        type: string
        value: "2025-08-15"
      - name: current-time
        type: string
        value: "15:17:59"
      successStatus: true
      successReason: Succeeded
status:
  startTime: %s`

const ttrManifestExpectsUndeclaredEnvStep = `
metadata:
  name: invalid-ttr-expects-undeclared-env-step
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expected:
      stepEnvs:
      - stepName: goodbye-step
        env:
        - name: HOME
          value: /root
      successStatus: true
      successReason: Succeeded
status:
  startTime: %s`

const ttrManifestExpectsUndeclaredFileSystemStep = `
metadata:
  name: invalid-ttr-expects-undeclared-fs-step
  namespace: foo
spec:
  timeout: 1h
  taskTestSpec:
    taskRef:
      name: task
    expected:
      fileSystemContents:
      - stepName: goodbye-step
        objects:
        - path: /tekton/results/current-date
          type: Directory
        - path: /tekton/results/current-time
          type: TextFile
          content: foo
      successStatus: true
      successReason: Succeeded
status:
  startTime: %s`

const ttrManifestAbsentTask = `
metadata:
  name: invalid-ttr-absent-task
  namespace: foo
spec:
  taskTestSpec:
    taskRef:
      name: absent-task`

// Valid TaskTestSuiteRun manifests
const ttsrManifestSimpleInlineTts = `
metadata:
  name: simple-ttsr-inline-tts
  namespace: foo
spec:
  taskTestSuiteSpec:
    taskTests:
    - name: task-0
      taskTestRef:
        name: task-test
    - name: task-1
      taskTestSpec:
        taskRef:
          name: task
`

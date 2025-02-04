# Sledge Operator

The Sledge Operator is a Kubernetes operator (built with [Kubebuilder] that manages Cloud SQL instances in GCP using the [`sledge` CLI](https://github.com/code4bread/sledge). This operator supports create, update, describe, and delete operations on Cloud SQL instances.

## Overview

- **CRD**: `CloudSQLInstance` defines the desired state of a Cloud SQL instance.
- **Controller**: On each reconcile, it calls `sledge` subcommands (`describe`, `create`, `update`, `delete`) to ensure the GCP resource matches the CR’s spec.
- **Finalizer**: Ensures that when you delete a `CloudSQLInstance` CR, `sledge delete` is called, preventing orphaned resources in GCP.

## Prerequisites

- **Go 1.20+** (or whichever version matches your Kubebuilder environment).
- **Docker** (for building container images).
- **Kubebuilder** installed.
- **Google Cloud** credentials (for `sledge` to interact with GCP).

## Building Sledge (Locally)

If you have the [Sledge](https://github.com/code4bread/sledge) code separately:

1. Clone or obtain sledge.
2. `go build -o sledge ./cmd/sledge`
3. Move the resulting `sledge` binary into this `sledge-operator` directory so Docker can copy it.

## Building & Deploying the Operator

1. **Generate manifests**:
   ```bash
   make generate
   make manifests
   ```

2. **Build** the Docker image (replacing with your registry):
   ```bash
   docker build -t your-registry/sledge-operator:latest .
   ```

3. **Usage**

    Create a CloudSQLInstance

    Write a file my-db.yaml:
    ```yaml
    apiVersion: cloudsql.uipath.studio/v1alpha1
    kind: CloudSQLInstance
    metadata:
      name: my-db
    spec:
      projectID: "my-gcp-project"
      instanceName: "my-db-instance"
      region: "us-central1"
      databaseVersion: "MYSQL_8_0"
      tier: "db-f1-micro"
    ```

4. **Apply It**

   ```bash  
   kubectl apply -f my-db.yaml
   ```

5. **Check Status** 
   ```bash
   kubectl get cloudsqlinstance my-db -o yaml
   ```

6. **Status**

    ```yaml
    status:
      phase: Ready
      message: "Instance is up-to-date"
      dbVersion: "MYSQL_8_0"
      state: "RUNNABLE"
      ipAddress: "104.154.xxx.xxx"
    ```

7. **Update**

   Change a field in spec (e.g., dbVersion or tier). The operator detects the difference and calls sledge update.

8. **Delete**

   The operator’s finalizer calls sledge delete before removing the CR from Kubernetes, preventing orphaned instances in GCP.
   ```bash
   kubectl delete cloudsqlinstance my-db
   ```

### Potential Issues & Notes

    1. **Valid JSON:** The operator expects `sledge describe` to output valid JSON without logs or extra text.
    2. **Credentials:** The operator container must have GCP credentials so sledge can authenticate.
    3. **Region Changes:** If `sledge update` doesn’t handle region changes, the operator may return an error when the region is altered.
    4. **Performance:** Each reconcile spawns a sledge process. For many CRs or frequent changes, consider caching or using slower requeues.
    5. **Exit Codes:** If sledge returns a non-zero exit code, the operator marks the CR in an error phase, logging the combined output.

 ## License

This project is licensed under the MIT License.

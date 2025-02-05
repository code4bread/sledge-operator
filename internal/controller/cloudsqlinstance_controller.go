package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	cloudsqlv1alpha1 "github.com/code4bread/sledge-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const cloudSQLFinalizer = "cloudsql.uipath.studio/finalizer"

// CloudSQLInstanceReconciler reconciles a CloudSQLInstance object
type CloudSQLInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SledgeDescribeOutput holds the JSON fields from sledge describe
type SledgeDescribeOutput struct {
	Name            string `json:"name"`
	Region          string `json:"region"`
	DatabaseVersion string `json:"databaseVersion"`
	State           string `json:"state"`
	Settings struct {
		Tier string `json:"tier"`
	  } `json:"settings"`
	IpAddresses     []struct {
		IPAddress string `json:"ipAddress"`
	} `json:"ipAddresses"`
}

// Reconcile is the main logic
func (r *CloudSQLInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr cloudsqlv1alpha1.CloudSQLInstance
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		// Ignore not-found errors since we can't do anything anyway
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Printf("Reconciling CloudSQLInstance %s\n %s\n", req.NamespacedName,cr.Spec)
     
	// 1. Check if being deleted
	if !cr.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &cr)
	}

	// 2. Ensure finalizer is present
	if !containsString(cr.Finalizers, cloudSQLFinalizer) {
		cr.Finalizers = append(cr.Finalizers, cloudSQLFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		// Return here so next reconcile sees finalizer in place
		return ctrl.Result{}, nil
	}

	// 3. Call sledge describe
	describeOut, err := r.sledgeDescribe(cr.Spec.ProjectID, cr.Spec.InstanceName)
	if err != nil {
		// If indicates not found => create
		if strings.Contains(err.Error(), "error describing instance") {
			log.Println("Instance not found, creating with sledge create...")
			if createErr := r.sledgeCreate(cr); createErr != nil {
				r.setStatusError(&cr, "ErrorCreating", createErr.Error())
				_ = r.Status().Update(ctx, &cr)
				return ctrl.Result{}, createErr
			}
			r.setStatusReady(&cr, "Instance created")
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{}, nil
		}
		// Another error
		r.setStatusError(&cr, "ErrorDescribe", err.Error())
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{}, err
	}

	// 4. Parse describe output into JSON struct
	var desc SledgeDescribeOutput
	if unErr := json.Unmarshal([]byte(describeOut), &desc); unErr != nil {
		log.Printf("Failed to parse JSON from sledge describe: %v\n%s\n", unErr, describeOut)
	} else {
		cr.Status.DBVersion = desc.DatabaseVersion
		cr.Status.State = desc.State
		if len(desc.IpAddresses) > 0 {
			cr.Status.IPAddress = desc.IpAddresses[0].IPAddress
		}
	}

	// 5. If the CloudSQL instance is in an in-progress state, we requeue
	switch desc.State {
	case "PENDING_CREATE", "MAINTENANCE", "BACKUP_IN_PROGRESS":
		cr.Status.Phase = "Pending"
		cr.Status.Message = fmt.Sprintf("Instance is in %s state; re-checking in 20s.", desc.State)
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{RequeueAfter: 20 * time.Second}, nil

	case "RUNNABLE":
		// It's fully operational
		cr.Status.Phase = "Ready"
		cr.Status.Message = "Instance is fully operational."

	default:
		// Some unknown or error state
		cr.Status.Phase = "Error"
		cr.Status.Message = fmt.Sprintf("Unexpected instance state: %s", desc.State)
	}

	_ = r.Status().Update(ctx, &cr)

	// 6. Decide if we need to call update, only if not pending or error
	if desc.State == "RUNNABLE" && r.needsUpdate(&cr, desc) {
		log.Println("Specs differ from actual. Updating with sledge update...")
		if upErr := r.sledgeUpdate(&cr); upErr != nil {
			r.setStatusError(&cr, "ErrorUpdating", upErr.Error())
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, upErr
		}
		r.setStatusReady(&cr, "Instance updated")
		_ = r.Status().Update(ctx, &cr)
	}

	return ctrl.Result{}, nil
}

// handleDeletion calls sledge delete if finalizer is present
func (r *CloudSQLInstanceReconciler) handleDeletion(ctx context.Context, cr *cloudsqlv1alpha1.CloudSQLInstance) (ctrl.Result, error) {
	if containsString(cr.Finalizers, cloudSQLFinalizer) {
		log.Printf("Deleting instance %s in GCP via sledge...\n", cr.Spec.InstanceName)
		if err := r.sledgeDelete(cr.Spec.ProjectID, cr.Spec.InstanceName); err != nil {
			log.Printf("Error deleting instance: %v", err)
			return ctrl.Result{}, err
		}
		// Remove finalizer
		cr.Finalizers = removeString(cr.Finalizers, cloudSQLFinalizer)
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		log.Printf("Instance %s deleted, finalizer removed.\n", cr.Spec.InstanceName)
	}
	return ctrl.Result{}, nil
}

// ------------------ sledge exec calls ------------------ //

func (r *CloudSQLInstanceReconciler) sledgeDescribe(project, instance string) (string, error) {
	cmd := exec.Command("sledge", "describe", "--project="+project, "--instance="+instance)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error describing instance %s: %v\n%s", instance, err, out)
	}
	return string(out), nil
}

func (r *CloudSQLInstanceReconciler) sledgeCreate(cr cloudsqlv1alpha1.CloudSQLInstance) error {
	args := []string{
		"create",
		"--project=" + cr.Spec.ProjectID,
		"--instance=" + cr.Spec.InstanceName,
		"--region=" + cr.Spec.Region,
		"--dbVersion=" + cr.Spec.DatabaseVersion,
		"--tier=" + cr.Spec.Tier,
	}
	out, err := exec.Command("sledge", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("error creating instance: %v\n%s", err, out)
	}
	log.Printf("sledge create output: %s\n", out)
	return nil
}

func (r *CloudSQLInstanceReconciler) sledgeUpdate(cr *cloudsqlv1alpha1.CloudSQLInstance) error {
	args := []string{
		"upgrade",
		"--project=" + cr.Spec.ProjectID,
		"--instance=" + cr.Spec.InstanceName,
		"--dbVersion=" + cr.Spec.DatabaseVersion,
		"--tier=" + cr.Spec.Tier,
	}
	out, err := exec.Command("sledge", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("error updating instance: %v\n%s", err, out)
	}
	log.Printf("sledge update output: %s\n", out)
	return nil
}

func (r *CloudSQLInstanceReconciler) sledgeDelete(project, instance string) error {
	args := []string{
		"delete",
		"--project=" + project,
		"--instance=" + instance,
	}
	out, err := exec.Command("sledge", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("error deleting instance %s: %v\n%s", instance, err, out)
	}
	log.Printf("sledge delete output: %s\n", out)
	return nil
}

// ------------------ utility methods ------------------ //

func (r *CloudSQLInstanceReconciler) needsUpdate(
	cr *cloudsqlv1alpha1.CloudSQLInstance,
	desc SledgeDescribeOutput,
) bool {
	log.Printf("Comparing desc.Settings.Tier=%q vs spec.Tier=%q, desc.DBVersion=%q vs spec.DBVersion=%q",
    desc.Settings.Tier, cr.Spec.Tier, desc.DatabaseVersion, cr.Spec.DatabaseVersion)

	if desc.DatabaseVersion != cr.Spec.DatabaseVersion {
		return true
	}
    if desc.Settings.Tier != cr.Spec.Tier {
        return true
    }
	return false
}

func (r *CloudSQLInstanceReconciler) setStatusReady(cr *cloudsqlv1alpha1.CloudSQLInstance, msg string) {
	cr.Status.Phase = "Ready"
	cr.Status.Message = msg
}

func (r *CloudSQLInstanceReconciler) setStatusError(cr *cloudsqlv1alpha1.CloudSQLInstance, phase, msg string) {
	cr.Status.Phase = phase
	cr.Status.Message = msg
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *CloudSQLInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudsqlv1alpha1.CloudSQLInstance{}).
		Complete(r)
}

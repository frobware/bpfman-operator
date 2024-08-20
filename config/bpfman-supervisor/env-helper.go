package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	deploymentName = "bpfman-operator"
	daemonsetName  = "bpfman-daemon"
	namespace      = "bpfman"
)

var lastDeploymentEnvContent string
var lastDaemonSetEnvContent string

func main() {
	var envDir string

	flag.StringVar(&envDir, "env-dir", "/etc/profile.d", "Base path to write environment files.")
	flag.Parse()

	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to local config.
		if home := homedir.HomeDir(); home != "" {
			configPath := filepath.Join(home, ".kube", "config")
			config, err = clientcmd.BuildConfigFromFlags("", configPath)
			if err != nil {
				fmt.Printf("Error creating local config: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Error creating in-cluster config: %v\n", err)
			os.Exit(1)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Create a list watcher for the deployments.
	deploymentListWatcher := cache.NewListWatchFromClient(
		clientset.AppsV1().RESTClient(),
		"deployments",
		namespace,
		fields.Everything(),
	)

	// Create a list watcher for the daemonsets.
	daemonSetListWatcher := cache.NewListWatchFromClient(
		clientset.AppsV1().RESTClient(),
		"daemonsets",
		namespace,
		fields.Everything(),
	)

	// Create an informer for deployments.
	deploymentInformer := cache.NewSharedIndexInformer(
		deploymentListWatcher,
		&v1.Deployment{},
		5*time.Second,
		cache.Indexers{},
	)

	// Create an informer for daemonsets.
	daemonSetInformer := cache.NewSharedIndexInformer(
		daemonSetListWatcher,
		&v1.DaemonSet{},
		5*time.Second,
		cache.Indexers{},
	)

	// Add event handlers to the deployment informer.
	deploymentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deployment := obj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				writeEnvFileFromDeployment(deployment, "AddFunc", filepath.Join(envDir, deploymentName+".sh"), clientset)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			deployment := newObj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				writeEnvFileFromDeployment(deployment, "UpdateFunc", filepath.Join(envDir, deploymentName+".sh"), clientset)
			}
		},
		DeleteFunc: func(obj interface{}) {
			deployment := obj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				fmt.Printf("Deployment %s/%s deleted. Event: DeleteFunc\n", namespace, deploymentName)
			}
		},
	})

	// Add event handlers to the daemonset informer.
	daemonSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			daemonSet := obj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				writeEnvFileFromDaemonSet(daemonSet, "AddFunc", filepath.Join(envDir, daemonsetName+".sh"), clientset)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			daemonSet := newObj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				writeEnvFileFromDaemonSet(daemonSet, "UpdateFunc", filepath.Join(envDir, daemonsetName+".sh"), clientset)
			}
		},
		DeleteFunc: func(obj interface{}) {
			daemonSet := obj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				fmt.Printf("DaemonSet %s/%s deleted. Event: DeleteFunc\n", namespace, daemonsetName)
			}
		},
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	go deploymentInformer.Run(stopCh)
	go daemonSetInformer.Run(stopCh)

	// Wait for signals to stop the program.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("Shutting down...")
}

func writeEnvFileFromDeployment(deployment *v1.Deployment, event, envFilePath string, clientset *kubernetes.Clientset) {
	envFileContent := extractEnvVarsFromDeployment(deployment, clientset)

	if envFileContent == lastDeploymentEnvContent {
		// fmt.Printf("No changes in environment variables. Skipping file write. Event: %s\n", event)
		return
	}

	err := os.WriteFile(envFilePath, []byte(envFileContent), 0644)
	if err != nil {
		fmt.Printf("Error writing to file: %v\n", err)
		os.Exit(1)
	}

	lastDeploymentEnvContent = envFileContent
	fmt.Printf("Deployment %s/%s environment variables written to %s (%s)\n", namespace, deploymentName, envFilePath, event)
}

func writeEnvFileFromDaemonSet(daemonSet *v1.DaemonSet, event, envFilePath string, clientset *kubernetes.Clientset) {
	envFileContent := extractEnvVarsFromDaemonSet(daemonSet, clientset)

	if envFileContent == lastDaemonSetEnvContent {
		// fmt.Printf("No changes in environment variables. Skipping file write. Event: %s\n", event)
		return
	}

	err := os.WriteFile(envFilePath, []byte(envFileContent), 0644)
	if err != nil {
		fmt.Printf("Error writing to file: %v\n", err)
		os.Exit(1)
	}

	lastDaemonSetEnvContent = envFileContent
	fmt.Printf("DaemonSet %s/%s environment variables written to %s (%s)\n", namespace, daemonsetName, envFilePath, event)
}

func generateKubernetesEnvVars(clientset *kubernetes.Clientset) string {
	var envVars []string

	// Get the list of services in the namespace.
	services, err := clientset.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error listing services: %v\n", err)
		return ""
	}

	for _, service := range services.Items {
		prefix := strings.ToUpper(service.Name) + "_SERVICE"
		prefix = strings.ReplaceAll(prefix, "-", "_")
		host := fmt.Sprintf("%s_HOST=%s", prefix, service.Spec.ClusterIP)
		port := fmt.Sprintf("%s_PORT=%d", prefix, service.Spec.Ports[0].Port)
		envVars = append(envVars, fmt.Sprintf("export %s", host))
		envVars = append(envVars, fmt.Sprintf("export %s", port))
	}

	// Add the Kubernetes service environment variables.
	envVars = append(envVars,
		"export KUBERNETES_SERVICE_PORT_HTTPS=443",
		"export KUBERNETES_SERVICE_PORT=443",
		"export KUBERNETES_PORT_443_TCP=tcp://10.96.0.1:443",
		"export KUBERNETES_PORT_443_TCP_PROTO=tcp",
		"export KUBERNETES_PORT_443_TCP_ADDR=10.96.0.1",
		"export KUBERNETES_SERVICE_HOST=10.96.0.1",
		"export KUBERNETES_PORT=tcp://10.96.0.1:443",
		"export KUBERNETES_PORT_443_TCP_PORT=443",
	)

	return strings.Join(envVars, "\n")
}

func extractEnvVarsFromDeployment(deployment *v1.Deployment, clientset *kubernetes.Clientset) string {
	var envFileContent string

	// Extract environment variables from the deployment.
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Value != "" {
				envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, env.Value)
			} else if env.ValueFrom != nil {
				value, err := resolveEnvValueFrom(env.ValueFrom, clientset, deployment.Namespace)
				if err == nil {
					envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, value)
				} else {
					fmt.Printf("Error resolving env var %s: %v\n", env.Name, err)
				}
			}
		}
	}

	// These are specified in the containerfile as explicit ENV
	// variables. (None at the moment.)

	// Add Kubernetes environment variables dynamically.
	envFileContent += generateKubernetesEnvVars(clientset)

	return envFileContent
}

func extractEnvVarsFromDaemonSet(daemonSet *v1.DaemonSet, clientset *kubernetes.Clientset) string {
	var envFileContent string

	// Extract environment variables from the daemonset.
	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Value != "" {
				envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, env.Value)
			} else if env.ValueFrom != nil {
				value, err := resolveEnvValueFrom(env.ValueFrom, clientset, daemonSet.Namespace)
				if err == nil {
					envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, value)
				} else {
					fmt.Printf("Error resolving env var %s: %v\n", env.Name, err)
				}
			}
		}
	}

	// These are specified in the containerfile as explicit ENV
	// variables. (None at the moment.)

	// Add Kubernetes environment variables dynamically.
	envFileContent += generateKubernetesEnvVars(clientset)

	return envFileContent
}

func resolveEnvValueFrom(valueFrom *corev1.EnvVarSource, clientset *kubernetes.Clientset, namespace string) (string, error) {
	if valueFrom.ConfigMapKeyRef != nil {
		// Handle ConfigMapKeyRef
		cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), valueFrom.ConfigMapKeyRef.Name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get configmap %s: %v", valueFrom.ConfigMapKeyRef.Name, err)
		}
		if value, exists := cm.Data[valueFrom.ConfigMapKeyRef.Key]; exists {
			return value, nil
		} else {
			return "", fmt.Errorf("key %s not found in configmap %s", valueFrom.ConfigMapKeyRef.Key, valueFrom.ConfigMapKeyRef.Name)
		}
	}

	if valueFrom.FieldRef != nil {
		switch valueFrom.FieldRef.FieldPath {
		case "spec.nodeName":
			nodeName := os.Getenv("HOSTNAME")
			if nodeName == "" {
				return "", fmt.Errorf("HOSTNAME environment variable is not set")
			}
			return nodeName, nil
		default:
			return "", fmt.Errorf("unsupported FieldRef: %s", valueFrom.FieldRef.FieldPath)
		}
	}

	// Handle other possible EnvVarSource types (e.g.,
	// SecretKeyRef, ResourceFieldRef) here if needed.

	return "", fmt.Errorf("unsupported EnvVarSource")
}

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	"encoding/json"
	"net/http"
	"bytes"
	"io"
	"sync"
	"regexp"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// watch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

)

func main() {
	// Accept optional kubeconfig flag
	kubeconfig := flag.String("kubeconfig", "", "Path to the kubeconfig file (optional if in-cluster)")
	namespace := flag.String("namespace", "app-namespace", "Namespace to watch (default is all)")
	ai_url := flag.String("ai_url", "", "url to access ai")
	flag.Parse()

	// Set up config
	config, err := getKubeConfig(*kubeconfig)
	if err != nil {
		panic(fmt.Errorf("failed to get kube config: %w", err))
	}

	// Create client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(fmt.Errorf("failed to create client: %w", err))
	}

	// Setup signal handling to gracefully stop
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	// Context for the watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-stopCh
		fmt.Println("Stopping...")
		cancel()
	}()

	// Start watching events
	fmt.Println("Starting event watcher...")
	watchEvents(ctx, clientset, *namespace, *ai_url)
}

func getKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	// Fallback to in-cluster config
	return rest.InClusterConfig()
}


var (
	errorBuffer   []string
	bufferMutex   sync.Mutex
	lastSent      time.Time
	bufferTimeout = 10 * time.Second
)

func flushErrors(ai_url string) {
	bufferMutex.Lock()
	defer bufferMutex.Unlock()

	if len(errorBuffer) == 0 {
		return
	}

	combined := ""
	for _, msg := range errorBuffer {
		combined += msg + "\n"
	}
	errorBuffer = nil
	sendAI(combined, ai_url)
}



func sendAI(message string, ai_url string) {

	// get manifest that has problems - since we only have 1 app it is this one
	manifest_url := "https://raw.githubusercontent.com/Hunh0w/Ramen/refs/heads/main/kube/nginx.yaml"
	manifest, err := http.Get(manifest_url)
	if err != nil {
		fmt.Printf("Failed to get manifest\n", err)
		return
	}
	defer manifest.Body.Close()
	manifestBody, _ := io.ReadAll(manifest.Body)


	final_message := "Here is the manifest of the deployed application in Kubernetes: \n```yaml\n" + string(manifestBody) + "\n```\n and here is the error:\n```\n" + message + "\n```\nPlease return the corrected manifest that I can apply. Please be very conscise. I only need the manifest. Please provide only the full corrected manifest."

	fmt.Printf(final_message)

	payload := map[string]interface{}{
		"model": "qwen2.5-coder-1.5b-cpu",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": final_message,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Failed to marshal payload: %v\n", err)
		return
	}

	url := ai_url + "/openai/v1/chat/completions"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("Failed to send request to KubeAi: %v\n", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println("\nKubeAi response:\n")
	// Regex to capture content between triple backticks and optional "yaml"
	re := regexp.MustCompile("(?s)```(?:yaml)?\\s*(.*?)\\s*```")
	matches := re.FindStringSubmatch(string(respBody))
	fmt.Println(matches)
	if len(matches) >= 2 {
		// get final yaml
		yaml := matches[1]
		fmt.Println(yaml)
	} else {
		fmt.Println("YAML block not found")
	}
}

func watchEvents(ctx context.Context, clientset *kubernetes.Clientset, namespace string, ai_url string) {
	watcher, err := clientset.CoreV1().Events(namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to start watch: %w", err))
	}
	defer watcher.Stop()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Context cancelled, exiting watch loop.")
			return
		case <-ticker.C:
			if time.Since(lastSent) >= bufferTimeout {
				flushErrors(ai_url)
				lastSent = time.Now()
			}
		case event, ok := <-watcher.ResultChan():
			if !ok {
				fmt.Println("Watch channel closed, reconnecting...")
				time.Sleep(2 * time.Second)
				watchEvents(ctx, clientset, namespace, ai_url) // Recurse for reconnect
				return
			}

			if e, ok := event.Object.(*v1.Event); ok && e.Type == v1.EventTypeWarning {
				msg := fmt.Sprintf("[%s] %s/%s: %s - %s",
					e.LastTimestamp.Format(time.RFC3339),
					e.Namespace, e.InvolvedObject.Name,
					e.Reason, e.Message)

				bufferMutex.Lock()
				errorBuffer = append(errorBuffer, msg)
				bufferMutex.Unlock()
			}			
		}
	}
}

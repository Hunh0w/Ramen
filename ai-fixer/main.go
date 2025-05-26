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
	"strings"
	"errors"
	"net/url"

	v1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	watch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"sigs.k8s.io/yaml"

	"github.com/google/go-github/v55/github"
	"golang.org/x/oauth2"
)

func main() {
	// Accept optional kubeconfig flag
	kubeconfig := flag.String("kubeconfig", "", "Path to the kubeconfig file (optional if in-cluster)")
	namespace := flag.String("namespace", "app-namespace", "Namespace to watch (default is all)")
	flag.Parse()
	ai_url := os.Getenv("AI_URL")

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
	watchEvents(ctx, clientset, *namespace, ai_url)
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

func int64Ptr(i int64) *int64 { return &i }

func extractYaml(message string) (string, error) {
	// Regex to capture content between triple backticks and optional "yaml"
	re := regexp.MustCompile("(?s)```(?:yaml)?\\s*(.*?)\\s*```")
	matches := re.FindStringSubmatch(message)
	if len(matches) >= 2 {
		// get final yaml
		yamlData := matches[1]
		yamlData = regexp.MustCompile(`\\n`).ReplaceAllString(yamlData, "\n")
		yamlData = regexp.MustCompile(`\\t`).ReplaceAllString(yamlData, "\t")
		yamlData = regexp.MustCompile(`\\\"`).ReplaceAllString(yamlData, "\"")
		yamlData = regexp.MustCompile(`\\\\`).ReplaceAllString(yamlData, "\\")
		yamlData = regexp.MustCompile(`^\s+|\s+$`).ReplaceAllString(yamlData, "")
		return yamlData, nil
	 } else {
		return "", errors.New("Message contains no yaml")
	}
}


func flushErrors(ctx context.Context, ai_url string, clientset *kubernetes.Clientset) string {
	bufferMutex.Lock()
	defer bufferMutex.Unlock()

	if len(errorBuffer) == 0 {
		return ""
	}

	combined := ""
	for _, msg := range errorBuffer {
		combined += msg + "\n"
	}
	errorBuffer = nil
	resp := sendAI(combined, ai_url)
	yamlData, err := extractYaml(resp)
	if (err == nil) {
		// Parse YAML into Deployment object
		var deploy appsv1.Deployment
		err := yaml.Unmarshal([]byte(yamlData), &deploy)
		if err != nil {
			fmt.Printf("Error parsing YAML: %v\n", err)
			return ""
		}
		
		// Apply updated yamlData
		deploymentsClient := clientset.AppsV1().Deployments(deploy.Namespace)

		// verify that it exists
		deployment, err := deploymentsClient.Get(ctx, deploy.Name, metav1.GetOptions{})
		if err == nil {
			// Update existing deployment
			deploy.ResourceVersion = deployment.ResourceVersion
			_, err = deploymentsClient.Update(ctx, &deploy, metav1.UpdateOptions{})
			if err != nil {
				fmt.Printf("Error applying deployment: %v\n", err)
				return ""
			}
			fmt.Printf("Deployment updated\nWatching it...")
			watcher, err := deploymentsClient.Watch(ctx, metav1.ListOptions{
				FieldSelector:  "metadata.name=" + deploy.Name,
				TimeoutSeconds: int64Ptr(30), // TODO change to 300
			})
			if err != nil {
				fmt.Printf("Can't watch deployment: %v\n", err)
				return ""
			}
			ready := int32(0)
			newerrors := combined + "\nAfter updating the deployment its status changed:\n"
			for event := range watcher.ResultChan() {
				d := event.Object.(*appsv1.Deployment)
				if (event.Type == watch.Error) {
					statusErr, ok := event.Object.(*metav1.Status)
					if ok {
						newerrors += statusErr.Message + "\n"
					}
				}
				ready = *d.Spec.Replicas - d.Status.ReadyReplicas
			}
			if (ready == 0) {
				fmt.Printf("Deployment has successfully been updated.\nCreating pull request.\n")
				createPullRequest("fix: ai correction", "kube/app_cluster/nginx.yaml", yamlData, "This PR proposes AI-generated fix for these errors: \n" + combined)
			} else {
				fmt.Printf("Deployment is still not working. Preparing to call the one in charge..")
				callHuman(newerrors)
			}

		} else {
			fmt.Printf("Deployment could not be found. %v\n", err)
			return ""
		}		
		return yamlData
	} else {
		fmt.Println("YAML block not found. %v\n", err)
		return ""
	}
}

func callHuman(kube_error string) {
	fmt.Printf("Calling..")
	phone_service_url := os.Getenv("PHONE_SERVICE_URL")
	resp, err := http.Get(phone_service_url + "/phone_call?text=" + url.QueryEscape(kube_error))
	defer resp.Body.Close()
	if err != nil {
		fmt.Printf("Call did not succeed.")
	}
	fmt.Printf("Call successful")
}

// cloneOrUpdateRepo either clones the repo if it doesn't exist, or pulls the latest changes
func cloneOrUpdateRepo(repoPath, repoURL, authToken string) (*git.Repository, error) {
	// Check if the directory exists
	if _, err := os.Stat(repoPath + "/.git"); err == nil {
		// Repo exists, open it
		fmt.Println("Repository exists, pulling latest changes...")
		repo, err := git.PlainOpen(repoPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open existing repo: %w", err)
		}

		w, err := repo.Worktree()
		if err != nil {
			return nil, fmt.Errorf("failed to get worktree: %w", err)
		}

		// Pull latest changes
		err = w.Pull(&git.PullOptions{
			RemoteName: "origin",
			Auth: &githttp.BasicAuth{
				Username: "ai-fixer", // can be anything non-empty
				Password: authToken,
			},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return nil, fmt.Errorf("failed to pull latest changes: %w", err)
		}

		fmt.Println("Repository updated successfully.")
		return repo, nil
	}

	// Repo doesn't exist, clone it
	fmt.Println("Cloning repository...")
	repo, err := git.PlainClone(repoPath, false, &git.CloneOptions{
		URL:      repoURL,
		Progress: os.Stdout,
		Auth: &githttp.BasicAuth{
			Username: "ai-fixer", // anything but must be non-empty
			Password: authToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to clone repo: %w", err)
	}
	fmt.Println("Repository cloned successfully.")
	return repo, nil
}

func createPullRequest(commitMessage string, fileName string, fileContent string, prMessage string) {
	// Setup variables
	repoURL := os.Getenv("GITHUB_URL")
	authToken := os.Getenv("GITHUB_TOKEN")
	now := time.Now()
	formatted := now.Format("02.01.06-15.04") // dd.mm.yy-hh.mm
	newBranch := "ai-fix/" + formatted
	repoPath := "./tmp-repo"

	// Clone repo
	repo, err := cloneOrUpdateRepo(repoPath, repoURL, authToken)

	if err != nil {
		fmt.Printf("Failed to clone repo: %v", err)
	}

	wt, _ := repo.Worktree()

	// Create new branch
	refName := plumbing.NewBranchReferenceName(newBranch)
	err = wt.Checkout(&git.CheckoutOptions{
		Branch: refName,
		Create: true,
	})
	if err != nil {
		fmt.Printf("Failed to checkout new branch: %v", err)
	}

	if strings.HasPrefix(fileContent, `\n`) {
		fileContent = fileContent[2:] // Remove first two characters
	}

	// Replace all \n (two-character sequence) with actual newline
	formattedContent := strings.ReplaceAll(fileContent, `\n`, "\n")

	// Write a file
	filePath := fmt.Sprintf("%s/%s", repoPath, fileName)
	err = os.WriteFile(filePath, []byte(formattedContent), 0644)
	if err != nil {
		fmt.Printf("Failed to write file: %v", err)
	}

	// Stage and commit
	_, err = wt.Add(fileName)
	if err != nil {
		fmt.Printf("Failed to add file: %v", err)
	}

	_, err = wt.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "AI Fixer",
			Email: "ai@fixer.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		fmt.Printf("Failed to commit: %v", err)
	}

	// Push the branch
	err = repo.Push(&git.PushOptions{
		Auth: &githttp.BasicAuth{
			Username: "ai-fixer",
			Password: authToken,
		},
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", newBranch, newBranch)),
		},
	})
	if err != nil {
		fmt.Printf("Failed to push: %v", err)
	}

	// Create pull request
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: authToken})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	newPR := &github.NewPullRequest{
		Title: github.String("PR: " + newBranch),
		Head:  github.String(newBranch), // same as the pushed branch
		Base:  github.String("main"),    // your target base branch
		Body:  github.String(prMessage),
	}

	pr, _, err := client.PullRequests.Create(ctx, "Hunh0w", "Ramen", newPR)
	if err != nil {
		fmt.Printf("Failed to create pull request: %v", err)
	}

	fmt.Printf("Pull request created: %s\n", pr.GetHTMLURL())
}

func sendAI(message string, ai_url string) (string) {

	// get manifest that has problems - since we only have 1 app it is this one
	manifest_url := "https://raw.githubusercontent.com/Hunh0w/Ramen/refs/heads/main/kube/app_cluster/nginx.yaml"
	manifest, err := http.Get(manifest_url)
	if err != nil {
		fmt.Printf("Failed to get manifest\n", err)
		return ""
	}
	defer manifest.Body.Close()
	manifestBody, _ := io.ReadAll(manifest.Body)


	final_message := "Here is the manifest of the deployed application in Kubernetes: \n```yaml\n" + string(manifestBody) + "\n```\n and here is the error:\n```\n" + message + "\n```\nPlease return the corrected manifest that I can apply. Please be very conscise. I only need the manifest. Please provide only the full corrected manifest."

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
		return ""
	}

	url := ai_url + "/openai/v1/chat/completions"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("Failed to send request to KubeAi: %v\n", err)
		return ""
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody)
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
				flushErrors(ctx, ai_url, clientset)
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

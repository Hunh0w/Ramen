# Projet Web

## Sujet

Développez un système scalable qui démarre un agent intelligent (hors cluster) capable de :

- Se connecter à un système de logs pour détecter les erreurs Kubernetes
- Tenter une résolution automatique de l'erreur via un modèle
- En cas de succès, ouvrir automatiquement une pull request
- En cas d'échec, passer un appel via IA à la personne d'astreinte
   - expliquer l'erreur à l'oral (le speech)
    - demander d'intervenir
    - Enregistrer dans un fichier:
        - l'heure de l'appel
        - le numéro appelé
        - le speech utilisé
        - la réponse (boolean) à la question, est ce qu'il peut intervenir?


## Schéma d'architecture

![diagramme d'architecture](./diagram.png)

Pour la suite, nous appellerons le cluster contenant l'ai deepseek "ramen" et celui contenant les applications et pods à observer "toobserve"

## Prérequis

avoir accès à deux cluster kubernetes  
nous l'avons testé avec k3d en local et helm d'installé.

**Pour rappel:**
créer 1 cluster qui s'appelle ramen:
```
k3d cluster create ramen
```

## Kubernetes cluster "ramen"

Le cluster contenant notre application et l'ia deepseek.

## KubeAi

Pour déployer notre ai nous allons utiliser [KubeAi](https://www.kubeai.org/).

```
helm repo add kubeai https://www.kubeai.org
helm repo update
helm install kubeai kubeai/kubeai --wait
helm install kubeai-models kubeai/models -f ./deepseek.yaml
kubectl port-forward svc/open-webui 8000:80
```

You can test that it worked by going to [localhost:8000](http://localhost:8000) and selecting the deepseek model.

You should be able to talk to an ai - the deepseek-r1-1.5b ai.
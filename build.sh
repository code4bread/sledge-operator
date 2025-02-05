 make generate
 make manifests
 docker build -t code4bread/sledge-operator:local_v10  .
 kind load docker-image code4bread/sledge-operator:local_v10 --name kind
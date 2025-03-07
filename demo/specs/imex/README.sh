###########################
#### Setup and Overview ###
###########################

# Look at the set of nodes on the cluster
kubectl get node

# Look at all pods running on the cluster
kubectl get pod -A

# Look at the set of nodes on the cluster and its <clusterUID.cliqueID> labels
(echo -e "NODE\tLABEL\tCLIQUE"; kubectl get nodes -o json | \
	jq -r '.items[] | [.metadata.name, "nvidia.com/gpu.clique", .metadata.labels["nvidia.com/gpu.clique"]] | @tsv') | \
	column -t

# Install the DRA driver for ComputeDomains
helm upgrade -i \
	--create-namespace \
	--namespace nvidia-dra-driver-gpu \
	nvidia-dra-driver-gpu \
	../../../deployments/helm/nvidia-dra-driver-gpu \
    --set nvidiaDriverRoot="/" \
    --set nvidiaCtkPath=/usr/local/nvidia/toolkit/nvidia-ctk \
	--set resources.gpus.enabled=false \
    --wait

# Show the DRA driver components running
kubectl get pod -n nvidia-dra-driver-gpu

# Show two MPI jobs, one traditional, and one referencing a ComputeDomain
vim -O nvbandwidth-test-no-compute-domain-job.yaml nvbandwidth-test-job.yaml

# Show the diff between the two MPI jobs
diff -ruN nvbandwidth-test-no-compute-domain-job.yaml nvbandwidth-test-job.yaml


#############################ä#########################
#### Run an MPI job together *with* a ComputeDomain ###
#############################ä#########################

# Create the ComputeDomain and Run the MPI job
kubectl apply -f nvbandwidth-test-job.yaml

# Look at the pods for the MPI job *within* a ComputeDomain
kubectl get pods

# Look at the pods for the IMEX daemons running on behalf of the MPI job *within* a ComputeDomain
kubectl get pods -n nvidia-dra-driver-gpu

# Look at the status of the newly created ComputeDomain
kubectl get -o yaml computedomains.resource.nvidia.com

# Look at the logs of the MPI job *within* a ComputeDomain
kubectl logs --tail=-1 -l job-name=nvbandwidth-test-launcher

# Verify workers and IMEX daemons already gone
kubectl get pod -A

# Delete the MPI job and its ComputeDomain
kubectl delete -f nvbandwidth-test-job.yaml


#################################################
#### Run an MPI job *without* a ComputeDomain ###
#################################################

# Run the MPI job
kubectl apply -f nvbandwidth-test-no-compute-domain-job.yaml

# Verify that no ComputeDomains exist
kubectl get -o yaml computedomains.resource.nvidia.com

# Look at the pods for the MPI job *without* a ComputeDomain
kubectl get pods

# Verify that no extra pods have been started in the nvidia namespace
kubectl get pods -n nvidia-dra-driver-gpu

# Look at the logs of the MPI job *without* a ComputeDomain
kubectl logs --tail=-1 -l job-name=nvbandwidth-test-launcher

# Delete the MPI job *without* a ComputeDomain
kubectl delete -f nvbandwidth-test-no-compute-domain-job.yaml

# Show everything cleaned up
kubectl get pod -A


####################################################
#### Run 2 MPI jobs with *unique* ComputeDomains ###
####################################################

# Show the diff of the original MPI job to one of these smaller MPI jobs
diff -ruN nvbandwidth-test-job.yaml nvbandwidth-test-job-1.yaml

# Run the 2 smaller MPI jobs
kubectl apply -f nvbandwidth-test-job-1.yaml
kubectl apply -f nvbandwidth-test-job-2.yaml

# Look at the pods for the 2 MPI jobs with *unique* ComputeDomains
kubectl get pods

# Look at the pods for the IMEX daemons running on behalf of the 2 MPI jobs with *unique* ComputeDomains
kubectl get pods -n nvidia-dra-driver-gpu

# Look at the status of the newly created ComputeDomain
kubectl get -o yaml computedomains.resource.nvidia.com

# Look at the logs of the first MPI job with a *unique* ComputeDomain
kubectl logs --tail=-1 -l job-name=nvbandwidth-test-1-launcher

# Look at the logs of the first MPI job with a *unique* ComputeDomain
kubectl logs --tail=-1 -l job-name=nvbandwidth-test-2-launcher

# Delete the 2 MPI jobs and their ComputeDomains
kubectl delete -f nvbandwidth-test-job-1.yaml
kubectl delete -f nvbandwidth-test-job-2.yaml

# Show everything cleaned up
kubectl get pod -A

#########################################
#### Queue up all 3 MPI jobs at once ####
#########################################

# Run all 3 MPI jobs
kubectl apply -f nvbandwidth-test-job.yaml
kubectl apply -f nvbandwidth-test-job-1.yaml
kubectl apply -f nvbandwidth-test-job-2.yaml

# Watch them all run to completion
kubectl get pod -A

# Run all 3 MPI jobs
kubectl delete -f nvbandwidth-test-job.yaml
kubectl delete -f nvbandwidth-test-job-1.yaml
kubectl delete -f nvbandwidth-test-job-2.yaml

# Verify everything is gone
kubectl get pod -A

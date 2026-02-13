# Machine Learning Example

Sandbox configuration for ML development with PyTorch, TensorFlow, or JAX.

## Setup

```bash
cd your-ml-project
cp /path/to/watermelon/docs/examples/python-ml/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
python -m venv venv
source venv/bin/activate
pip install torch transformers jupyter
jupyter notebook --ip=0.0.0.0 --port=8888
# Visit http://localhost:8888 on your host
```

## Resource Allocation

ML workloads need more resources. Adjust based on your host capacity:

```toml
[resources]
memory = "32GB"  # For larger models
cpus = 8
disk = "100GB"   # For datasets
```

## Model Downloads

Models from Hugging Face Hub are allowed by default. For other sources:

```toml
[network]
allow = [
    "pypi.org",
    "files.pythonhosted.org",
    "huggingface.co",
    "*.huggingface.co",
    "download.pytorch.org",        # PyTorch model zoo
    "storage.googleapis.com",      # TensorFlow Hub
]
```

## GPU Support

GPU passthrough requires additional Lima configuration. See Lima documentation for GPU setup.

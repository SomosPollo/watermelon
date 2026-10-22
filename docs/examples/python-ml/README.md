# Machine Learning Example

Sandbox configuration for CPU-based Python ML development. The example uses
automatic Python setup so libraries and Jupyter share the project environment.

## Setup

```bash
cd your-ml-project
cp /path/to/watermelon/docs/examples/python-ml/.watermelon.toml ./
```

Declare the packages you need in `requirements.txt` or a supported
`pyproject.toml`. For a new PyTorch notebook project, a requirements file could
contain:

```text
torch
transformers
jupyter
```

Review the settings and resource allocation, then run on the host:

```bash
watermelon config validate
watermelon up
watermelon run
```

## Inside the sandbox

```bash
jupyter notebook --no-browser --ip=0.0.0.0 --port=8888 --allow-root
```

Open the token URL printed by Jupyter, using `http://127.0.0.1:8888` on the
host. Keep the sandbox session open. The tool container is rootless in the VM,
but Jupyter sees container UID 0 and requires `--allow-root`; this does not grant
host root privileges. Automatic setup creates the Python environment, so no
extra activation step is needed.

## Resource Allocation

The example allocates 8 GB of memory, 4 CPUs, and a 30 GB disk. Adjust these
before the first run to fit your host and workload. Larger models may need:

```toml
[resources]
memory = "32GB"  # For larger models
cpus = 8
disk = "100GB"   # For datasets
```

## Model Downloads

The example allowlist includes Hugging Face Hub. Add only the other model sources your project needs:

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

This recipe tests CPU execution. It does not configure GPU access. GPU support
depends on your host, VM backend, and framework; installing a GPU-enabled
Python package alone does not expose the host GPU to this VM.

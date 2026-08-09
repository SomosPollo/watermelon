# Django Example

Sandbox configuration for Django web development.

## Setup

```bash
cd your-django-project
cp /path/to/watermelon/docs/examples/python-django/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
python manage.py runserver 0.0.0.0:8000
# Visit http://localhost:8000 on your host
```

## Database

For local SQLite development, no changes needed - the database file lives in your project.

For PostgreSQL/MySQL in another container:

```toml
[network]
allow = [
    "pypi.org",
    "files.pythonhosted.org",
    # Add your database host if external
]
```

## What's protected

Python packages with native extensions can run arbitrary code during installation. Watermelon ensures that code can't:
- Read your SSH keys or cloud credentials
- Directly write unmounted host persistence locations such as cron or shell profiles

The example's strict policy also blocks new non-allowlisted external traffic. Workload DNS is redirected to a managed resolver that answers only policy names, while loopback, established responses, and scoped VM-control DHCPv4 lease traffic remain available. The DHCP exception is not arbitrary external UDP access, and destinations allowed by policy can still carry data.

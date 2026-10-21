import json, os, pathlib, re, subprocess, sys
with open('/watermelon-runtime/projects.json') as stream:
    config = json.load(stream)
name = sys.argv[1]
if name not in ('uv', 'poetry'):
    raise SystemExit('Unknown package manager')
cwd = pathlib.Path.cwd()
jobs = [job for job in config['projects'] if job.get('manager', '').startswith(name + '@')]
jobs.sort(key=lambda job: len(job['directory']), reverse=True)
def matches(job):
    directory = pathlib.Path(config['root'], job['directory'])
    return cwd == directory or directory in cwd.parents
job = next((job for job in jobs if matches(job)), jobs[0] if len(jobs) == 1 else None)
if job is None:
    raise SystemExit('Choose a project directory with --cwd before invoking ' + name)
version = job['manager'].split('@')[1]
if not re.fullmatch(r'\d+\.\d+\.\d+', version):
    raise SystemExit('Invalid package manager version')
prefix = pathlib.Path(os.environ['HOME'], '.watermelon-tools', name+'-'+version+'-'+'.'.join(map(str,sys.version_info[:2])))
executable = prefix / 'bin' / name
if not executable.exists():
    subprocess.run(['/usr/local/bin/python', '-I', '-m', 'venv', str(prefix)], check=True)
    subprocess.run([str(prefix/'bin/python'), '-I', '-m', 'pip', 'install', '--disable-pip-version-check', '--index-url', 'https://pypi.org/simple', name+'=='+version], check=True)
env = os.environ.copy()
# A Poetry tool environment must not be mistaken for the project environment.
env.pop('VIRTUAL_ENV', None)
env['POETRY_VIRTUALENVS_IN_PROJECT'] = 'true'
env['POETRY_VIRTUALENVS_CREATE'] = 'true'
env['POETRY_NO_INTERACTION'] = '1'
env['UV_PYTHON_DOWNLOADS'] = 'never'
os.execve(str(executable), [str(executable)] + sys.argv[2:], env)

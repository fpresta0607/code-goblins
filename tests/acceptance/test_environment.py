import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import time

NATIVE_COMMIT = '3749169495e3999c1134ffd261806b701946bfbf'
NATIVE_TREE = '90f6e0967c85beb2c6830805b74834465dded17a'
COMMAND = 'uv run --extra dev pytest && node --test tests/js/*.test.mjs'
CREATE_NO_WINDOW = 0x08000000
STARTED = time.monotonic()
DEADLINE = STARTED + 20 * 60
CLEANUP_SECONDS = 120
PROCESS_RECORDS = []
PROCESS_HANDLES = []
PROCESS_LOG = None


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding='utf-8')


def remaining(is_cleanup=False):
    seconds = DEADLINE - time.monotonic() - (0 if is_cleanup else CLEANUP_SECONDS)
    if seconds <= 0:
        raise TimeoutError('The single 20-minute fixture deadline has expired.')
    return seconds


def track(process, program):
    record = {'pid': process.pid, 'program': str(program), 'started_monotonic': time.monotonic()}
    PROCESS_RECORDS.append(record)
    PROCESS_HANDLES.append(process)
    if PROCESS_LOG is not None:
        with PROCESS_LOG.open('a') as target:
            target.write(json.dumps(record) + '\n')
    return record


def run(arguments, directory, environment, check=True, is_cleanup=False):
    arguments = [str(value) for value in arguments]
    timeout = min(60 if is_cleanup else 180, remaining(is_cleanup))
    with subprocess.Popen(arguments, cwd=directory, env=environment, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True, creationflags=CREATE_NO_WINDOW) as process:
        record = track(process, arguments[0])
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate(timeout=min(10, remaining(True)))
            raise
        finally:
            record['exit_code'] = process.poll()
        result = subprocess.CompletedProcess(arguments, process.returncode, stdout, stderr)
        if check:
            result.check_returncode()
        return result


def native_result(home, run_id):
    with sqlite3.connect(f'file:{home / "state.sqlite"}?mode=ro', uri=True, timeout=min(5, remaining())) as database:
        database.row_factory = sqlite3.Row
        record = database.execute('SELECT runs.*,repos.working_path FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.id=?', (run_id,)).fetchone()
        if record is None:
            raise RuntimeError('The bound private fixture run is missing: ' + run_id)
        test = database.execute('SELECT status,exit_code,findings_json FROM step_results WHERE run_id=? AND step_name=?',
                                (record['id'], 'test')).fetchone()
        unrelated = database.execute("SELECT step_name FROM step_results WHERE run_id=? AND step_name!='test' AND status NOT IN ('pending','running','completed','skipped')", (run_id,)).fetchall()
        if unrelated:
            raise RuntimeError('An unrelated private native step failed or parked: ' + str([row['step_name'] for row in unrelated]))
        return dict(record), dict(test) if test is not None else None


def bind_native_run(home, branch, project, head, client=None):
    while True:
        with sqlite3.connect(f'file:{home / "state.sqlite"}?mode=ro', uri=True, timeout=min(5, remaining())) as database:
            rows = database.execute('SELECT runs.id,runs.repo_id FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.branch=? AND repos.working_path=? AND runs.submitted_head_sha=?',
                                    (branch, str(project), head)).fetchall()
        if len(rows) == 1:
            binding = {'id': rows[0][0], 'repo_id': rows[0][1], 'branch': branch, 'working_path': str(project),
                       'submitted_head_sha': head, 'head_sha': head, 'no_mistakes_build_sha': NATIVE_COMMIT}
            write(home.parent / (branch.rsplit('/', 1)[-1] + '-binding.json'), json.dumps(binding, indent=2))
            return binding
        if rows or client is None or client.poll() is not None:
            raise RuntimeError('The private native client did not create one unambiguous source-bound run.')
        time.sleep(min(.25, remaining()))


def observe_native_run(native, home, binding, directory, environment, root, scenario, is_success=False, is_waiting=True):
    while True:
        identity = json.loads((root / 'daemon-identity.json').read_text())
        daemon_status = run([native, 'daemon', 'status'], directory, environment)
        if json.loads((home / 'daemon.pid').read_text()) != identity or '(pid ' + str(identity['pid']) + ')' not in daemon_status.stdout:
            raise RuntimeError('The bound private daemon identity is no longer live.')
        status = run([native, 'axi', 'status', '--run', binding['id']], directory, environment)
        with (root / (scenario + '-status.log')).open('a') as target:
            target.write(status.stdout + status.stderr + '\n')
        lines = status.stdout.splitlines()
        if not lines or lines[0] != 'run:':
            raise RuntimeError('The public status did not select the bound private branch/run.')
        fields = []
        for line in lines[1:]:
            if line and not line.startswith(' '):
                break
            fields.append(line)
        for key in ('id', 'branch', 'head_sha'):
            if not any('  ' + key + ': ' + value in fields for value in (binding[key], json.dumps(binding[key]))):
                raise RuntimeError('The public status identity changed: ' + key)
        record, test = native_result(home, binding['id'])
        if any(record[key] != value for key, value in binding.items()):
            raise RuntimeError('The bound private run identity or source changed.')
        expected_worktree = str(home / 'worktrees' / binding['repo_id'] / binding['id'])
        if record['worktree_dir'] != expected_worktree or record['status'] not in ('pending', 'running', 'completed') or record['error']:
            raise RuntimeError('The bound private run was cancelled, failed or has unknown source/state.')
        if test is None or test['status'] not in ('pending', 'running', 'completed', 'awaiting_approval', 'failed'):
            raise RuntimeError('The bound private Test has unknown or unrelated state.')
        is_observed = (record['status'] == 'completed' if is_success else
                       test is not None and test['status'] in ('completed', 'awaiting_approval', 'failed') and test['exit_code'] is not None)
        if is_observed or not is_waiting:
            return record, test
        if record['status'] == 'completed' or record['awaiting_agent_since'] is not None:
            raise RuntimeError('The private run parked before its required observation existed.')
        print('Observing same private run ' + binding['id'] + ': ' + record['status'], flush=True)
        time.sleep(min(2, remaining()))


def events(root, scenario):
    path = root / 'events' / (scenario + '.jsonl')
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def main():
    global PROCESS_LOG
    parser = argparse.ArgumentParser()
    parser.add_argument('--native-directory', type=Path, required=True)
    parser.add_argument('--consumer', type=Path, required=True)
    parser.add_argument('--root', type=Path, required=True)
    arguments = parser.parse_args()
    native_directory, consumer, root = (path.resolve() for path in (arguments.native_directory, arguments.consumer, arguments.root))
    native = native_directory / 'no-mistakes.exe'
    receipt = json.loads((native_directory / 'receipt.json').read_text())
    if receipt['source_commit'] != NATIVE_COMMIT or receipt['source_tree'] != NATIVE_TREE:
        raise RuntimeError('The native candidate is not the exact reviewed and merged source.')
    for name, field in [('no-mistakes.exe', 'native_binary_sha256'), ('codex.exe', 'fakeagent_binary_sha256')]:
        if hashlib.sha256((native_directory / name).read_bytes()).hexdigest() != receipt[field]:
            raise RuntimeError('The source-bound native artifact changed: ' + name)
    consumer_receipt = json.loads((consumer.parent / 'consumer-receipt.json').read_text())
    if hashlib.sha256(consumer.read_bytes()).hexdigest() != consumer_receipt['binary_sha256']:
        raise RuntimeError('The source-bound consumer artifact changed.')
    if root.exists():
        raise RuntimeError('Use a new proof root and preserve every existing fixture history.')
    root.mkdir(parents=True)
    PROCESS_LOG = root / 'processes.jsonl'
    home, native_home, checkout = root / 'fleet', root / 'native', root / 'sample-project'
    owner, upstream = home / 'worktrees' / checkout.name / 'proof-task', root / 'upstream.git'
    for directory in (home / 'state', native_home, checkout, root / 'events', root / 'bin',
                      root / 'user' / 'AppData' / 'Local', root / 'user' / 'AppData' / 'Roaming',
                      root / 'appdata', root / 'localappdata', root / 'temp', root / 'verification'):
        directory.mkdir(parents=True, exist_ok=True)
    for name in ('codex.exe', 'gh.exe'):
        shutil.copyfile(native_directory / 'codex.exe', root / 'bin' / name)
    permitted = {'systemroot', 'windir', 'comspec', 'pathext', 'path', 'number_of_processors',
                 'processor_architecture', 'pip_uploaded_prior_to', 'uv_exclude_newer', 'npm_config_before'}
    environment = {key: value for key, value in os.environ.items() if key.lower() in permitted}
    environment.update({
        'PATH': str(root / 'bin') + os.pathsep + os.environ['PATH'],
        'USERPROFILE': str(root / 'user'), 'APPDATA': str(root / 'appdata'),
        'LOCALAPPDATA': str(root / 'localappdata'), 'TEMP': str(root / 'temp'), 'TMP': str(root / 'temp'),
        'CFO_HOME': str(home), 'CFO_STATE_OVERRIDE': str(home / 'state'),
        'CFO_VERIFY_DIR': str(root / 'verification'), 'NM_HOME': str(native_home),
        'NM_TEST_START_DAEMON': '1', 'NM_TEST_DAEMON_START_TIMEOUT': '45s',
        'NO_MISTAKES_TELEMETRY': 'off', 'NO_MISTAKES_NO_UPDATE_CHECK': '1',
        'FAKEAGENT_LOG': str(root / 'agent.log'), 'UV_PROJECT_ENVIRONMENT': 'daemon-unverified',
        'UV_NO_SYNC': '0', 'PROOF_ROOT': str(root), 'PROOF_BASE_PYTHON': sys.executable,
        'GIT_CONFIG_GLOBAL': str(root / 'gitconfig'), 'GIT_CONFIG_NOSYSTEM': '1',
    })
    write(root / 'gitconfig', '[user]\n name = Configured runner proof\n email = proof@example.invalid\n[commit]\n gpgsign = false\n')
    wrapper = [str(consumer), 'gate', 'turn', '--gate-run', '{run}', '--test-comparison-sha',
               '{test_comparison_sha}', '--as', '{repo} {branch}, run {run_short}', '--']
    write(native_home / 'config.yaml', 'agent: codex\nlog_level: debug\nagent_path_override:\n  codex: ' +
          json.dumps(str(root / 'bin' / 'codex.exe')) + '\nauto_fix:\n  review: 0\n  test: 0\n  lint: 0\n  rebase: 0\n  document: 0\n  ci: 0\n' +
          'test_command_wrapper:\n  command: ' + json.dumps(wrapper) + '\n  refusal_prefix: "cfo gate turn: "\n')
    write(root / 'command-sentinel.py', SENTINEL)
    for tool in ('uv', 'node'):
        write(root / 'bin' / (tool + '.cmd'), '@echo off\n"%PROOF_BASE_PYTHON%" "%PROOF_ROOT%\\command-sentinel.py" ' + tool + ' %*\nexit /b %errorlevel%\n')
    write(root / 'bin' / 'proof-isolation.cmd', '@echo off\n"%PROOF_BASE_PYTHON%" "%PROOF_ROOT%\\command-sentinel.py" isolation\nexit /b %errorlevel%\n')
    git = lambda *values, directory=checkout: run(['git', *values], directory, environment).stdout.strip()
    git('init', '--bare', '--initial-branch=main', upstream, directory=root)
    git('init', '--initial-branch=main')
    write(checkout / '.no-mistakes.yaml', 'disable_project_settings: true\ncommands:\n  test: ' + json.dumps(COMMAND) + '\ntest:\n  base_attribution: true\n')
    write(checkout / 'README.txt', 'isolated daemon boundary proof\n')
    write(checkout / '.gitignore', '.venv/\n')
    git('add', '.no-mistakes.yaml', 'README.txt', '.gitignore')
    git('commit', '-m', 'chore(proof): configure trusted test command')
    comparison = git('rev-parse', 'HEAD')
    git('remote', 'add', 'origin', upstream.as_uri())
    git('push', '-u', 'origin', 'main')
    manifest = home / 'data' / 'projects' / checkout.name / 'project.json'
    write(manifest, json.dumps({'project': checkout.name, 'test_environment': {'python_version': '3.12.10'}}))
    write(home / 'state' / 'proof-task.meta', f'id=proof-task\nproject={checkout}\nworktree={owner}\n')
    write(root / 'intent.txt', 'Drive this isolated tracked fixture through its real configured Test runner using deterministic agents and command sentinels. Preserve the trusted command and actual exit result.\n')
    proof = {'native_source_commit': NATIVE_COMMIT, 'native_source_tree': NATIVE_TREE,
             'native_binary_sha256': receipt['native_binary_sha256'],
             'consumer_source_commit': consumer_receipt['source_commit'],
             'consumer_source_tree': consumer_receipt['source_tree'],
             'consumer_binary_sha256': consumer_receipt['binary_sha256'],
             'trusted_command': COMMAND, 'expected_fixture_comparison': comparison,
             'isolated_configured_daemon_proof_passed': False, 'shared_daemon_changed': False,
             'dependency_resolution_attempted': False, 'dependency_installation_attempted': False,
             'interpreter_download_attempted': False, 'overall_execution_bound_seconds': 1200,
             'cleanup_reserve_seconds': CLEANUP_SECONDS, 'fixture_pid': os.getpid(), 'bound_runs': {}}
    daemon_pid, holders = None, []
    try:
        initialized = run([native, 'init'], checkout, environment)
        write(root / 'init.log', initialized.stdout + initialized.stderr)
        daemon_identity = json.loads((native_home / 'daemon.pid').read_text())
        daemon_pid = daemon_identity['pid']
        proof['isolated_daemon_identity'] = daemon_identity
        write(root / 'daemon-identity.json', json.dumps(daemon_identity, indent=2))
        proof['isolated_daemon_pid'] = daemon_pid
        git('worktree', 'add', '-b', 'feat/binding-success', owner)
        run([sys.executable, '-m', 'venv', '--without-pip', owner / '.venv'], root, environment)
        environment_before = {str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in
                              (owner / '.venv' / 'pyvenv.cfg', owner / '.venv' / 'Scripts' / 'python.exe')}
        client_environment = dict(environment, ARBITRARY_CLIENT_TOKEN='client-only-sentinel',
                                  UV_PROJECT_ENVIRONMENT='client-wrong-project', UV_NO_SYNC='0')
        for scenario, version, code in [('success', '3.12.10', 0), ('failure', '3.12.10', 13), ('refusal', '3.12.99', 0), ('admission', '3.12.10', 0)]:
            if scenario != 'success':
                git('switch', '-c', 'feat/binding-' + scenario, directory=owner)
            write(owner / 'feature.txt', scenario + '\n')
            git('add', 'feature.txt', directory=owner)
            git('commit', '-m', 'test(proof): exercise ' + scenario, directory=owner)
            head = git('rev-parse', 'HEAD', directory=owner)
            write(manifest, json.dumps({'project': checkout.name, 'test_environment': {'python_version': version}}))
            write(root / 'exit-code.txt', str(code))
            command = [str(native), 'axi', 'run', '--intent-file', str(root / 'intent.txt'), '--wait', '90s']
            if scenario == 'admission':
                write(root / 'hold.py', 'import pathlib,sys,time\np=pathlib.Path(__file__).parent\n(p/("holder-started-"+sys.argv[1])).touch()\nwhile not (p/"holder-release").exists(): time.sleep(.05)\n')
                with (root / 'holder.log').open('w') as holder_log, (root / 'admission-drive.log').open('w') as drive_log:
                    for number in range(2):
                        holders.append(subprocess.Popen([str(consumer), 'gate', 'turn', '--as', 'admission holder ' + str(number), '--', sys.executable, str(root / 'hold.py'), str(number)],
                                                        cwd=root, env=environment, stdout=holder_log, stderr=subprocess.STDOUT,
                                                        creationflags=CREATE_NO_WINDOW))
                        track(holders[-1], 'admission holder ' + str(number))
                    while True:
                        remaining()
                        line = json.loads(run([consumer, 'gate', 'turns', '--json'], root, environment).stdout)
                        registered = sum('admission holder ' in entry['who'] for entry in line['holding'] + line['waiting'])
                        if any((root / ('holder-started-' + str(number))).exists() for number in range(2)) and registered == 2:
                            break
                        time.sleep(.1)
                    driving = subprocess.Popen(command, cwd=owner, env=client_environment, stdout=drive_log,
                                               stderr=subprocess.STDOUT, creationflags=CREATE_NO_WINDOW)
                    track(driving, native)
                    try:
                        binding = bind_native_run(native_home, 'feat/binding-' + scenario, checkout, head, driving)
                        proof['bound_runs'][scenario] = binding
                        while True:
                            remaining()
                            line = json.loads(run([consumer, 'gate', 'turns', '--json'], root, environment).stdout)
                            if any('binding-admission' in waiting['who'] for waiting in line['waiting']):
                                break
                            if driving.poll() is not None:
                                record, test = observe_native_run(native, native_home, binding, owner, environment, root, scenario, is_waiting=False)
                                client_output = (root / 'admission-drive.log').read_text()
                                is_client_hold = driving.returncode == 1 and 'wait of 1m30s elapsed while driving the run' in client_output
                                if driving.returncode != 0 and not is_client_hold or record['status'] != 'running' or test['exit_code'] is not None:
                                    raise RuntimeError('The native admission client ended without an active bound run.')
                            time.sleep(.25)
                        (owner / '.venv').rename(owner / '.venv-unavailable')
                        (root / 'holder-release').touch()
                        for holder in holders:
                            if holder.wait(timeout=min(30, remaining())) != 0:
                                raise RuntimeError('An admission holder failed.')
                        holders.clear()
                        drive_exit = driving.wait(timeout=min(180, remaining()))
                    finally:
                        (root / 'holder-release').touch()
                        if driving.poll() is None:
                            driving.terminate()
                            driving.wait(timeout=min(30, remaining(True)))
                        if (owner / '.venv-unavailable').exists():
                            (owner / '.venv-unavailable').rename(owner / '.venv')
            else:
                driven = run(command, owner, client_environment, check=False)
                drive_exit = driven.returncode
                write(root / (scenario + '-drive.log'), driven.stdout + driven.stderr)
                binding = bind_native_run(native_home, 'feat/binding-' + scenario, checkout, head)
                proof['bound_runs'][scenario] = binding
            client_output = (root / (scenario + '-drive.log')).read_text()
            is_client_hold = drive_exit == 1 and 'wait of 1m30s elapsed while driving the run' in client_output
            if drive_exit != 0 and not is_client_hold:
                raise RuntimeError('The native client failed outside its bounded hold contract.')
            proof[scenario + '_client_bounded_hold'] = is_client_hold
            proof[scenario + '_drive_exit'] = drive_exit
            record, test = observe_native_run(native, native_home, binding, owner, environment, root, scenario, is_success=scenario == 'success')
            assert record['no_mistakes_build_sha'] == NATIVE_COMMIT, record['no_mistakes_build_sha']
            write(root / (scenario + '-run.json'), json.dumps(record, indent=2))
            write(root / (scenario + '-test.json'), json.dumps(test, indent=2))
            observed = events(root, scenario)
            proof[scenario + '_run'] = record['id']
            proof[scenario + '_command_events'] = len(observed)
            if scenario == 'success':
                proof['success_previous_push_sha'] = record['base_sha']
                assert [event['tool'] for event in observed] == ['uv', 'node'], observed
                assert record['status'] == 'completed' and test['exit_code'] == 0, (record, test)
            elif scenario == 'failure':
                assert observed and all(event['tool'] == 'uv' for event in observed), observed
                assert test['exit_code'] == 13 and record['status'] != 'completed', (record, test)
                base = events(root, 'base')
                assert base and all(event['head'] == comparison for event in base), base
                assert all(event['prefix'] == str(owner / '.venv') and event['client_token'] is None for event in base), base
                proof['native_base_command_events'] = len(base)
            else:
                assert not observed and record['status'] != 'completed' and test['exit_code'] == 126, (observed, record, test)
                assert 'required test environment' in test['findings_json'], test
            for event in observed:
                assert event['head'] == record['head_sha'] and not event['gate_has_venv'], event
                assert event['client_token'] is None and event['prefix'] == str(owner / '.venv'), event
                assert event['version'] == '3.12.10' and event['releaselevel'] == 'final' and event['serial'] == 0, event
        other, other_upstream = root / 'other-project', root / 'other-upstream.git'
        other.mkdir()
        git('init', '--bare', '--initial-branch=main', other_upstream, directory=root)
        git('init', '--initial-branch=main', directory=other)
        other_command = 'proof-isolation'
        write(other / '.no-mistakes.yaml', 'disable_project_settings: true\ncommands:\n  test: ' + json.dumps(other_command) + '\n')
        write(other / '.gitignore', '.venv/\n')
        write(other / '.venv' / 'environment-marker', 'other-project-environment-unchanged')
        git('add', '.no-mistakes.yaml', '.gitignore', directory=other)
        git('commit', '-m', 'test(proof): configure independent project', directory=other)
        git('remote', 'add', 'origin', other_upstream.as_uri(), directory=other)
        git('push', '-u', 'origin', 'main', directory=other)
        run([native, 'init'], other, environment)
        git('switch', '-c', 'feat/isolation', directory=other)
        write(other / 'feature.txt', 'project isolation\n')
        git('add', 'feature.txt', directory=other)
        git('commit', '-m', 'test(proof): exercise project isolation', directory=other)
        head = git('rev-parse', 'HEAD', directory=other)
        isolated = run([native, 'axi', 'run', '--intent-file', root / 'intent.txt', '--wait', '90s'], other, client_environment, check=False)
        write(root / 'isolation-drive.log', isolated.stdout + isolated.stderr)
        binding = bind_native_run(native_home, 'feat/isolation', other, head)
        proof['bound_runs']['isolation'] = binding
        is_client_hold = isolated.returncode == 1 and 'wait of 1m30s elapsed while driving the run' in isolated.stdout + isolated.stderr
        if isolated.returncode != 0 and not is_client_hold:
            raise RuntimeError('The independent-project client failed outside its bounded hold contract.')
        proof['isolation_drive_exit'] = isolated.returncode
        proof['isolation_client_bounded_hold'] = is_client_hold
        isolated_record, isolated_test = observe_native_run(native, native_home, binding, other, environment, root, 'isolation', is_success=True)
        assert isolated_record['no_mistakes_build_sha'] == NATIVE_COMMIT, isolated_record['no_mistakes_build_sha']
        assert isolated_record['status'] == 'completed' and isolated_test['exit_code'] == 0, (isolated_record, isolated_test)
        proof['other_project_run'] = isolated_record['id']
        assert json.loads((root / 'isolation.json').read_text()) == {'environment': 'daemon-unverified', 'no_sync': '0', 'client_token': None}
        assert (other / '.venv' / 'environment-marker').read_text() == 'other-project-environment-unchanged'
        assert environment_before == {path: hashlib.sha256(Path(path).read_bytes()).hexdigest() for path in environment_before}
        assert not (root / 'resolver-install-download-attempted').exists()
        proof['other_project_and_client_credential_isolation_verified'] = True
        proof['verified_environment_unchanged'] = True
        proof['isolated_configured_daemon_proof_passed'] = True
    finally:
        try:
            (root / 'holder-release').touch()
            for holder in holders:
                try:
                    holder.wait(timeout=min(10, remaining(True)))
                except subprocess.TimeoutExpired:
                    proof['isolated_configured_daemon_proof_passed'] = False
                    holder.terminate()
                    holder.wait(timeout=min(10, remaining(True)))
        finally:
            is_cleanup_complete = False
            try:
                if daemon_pid is not None or (native_home / 'daemon.pid').exists():
                    stopped = run([native, 'daemon', 'stop', '--force'], root, environment, check=False, is_cleanup=True)
                    write(root / 'daemon-stop.log', stopped.stdout + stopped.stderr)
                    proof['isolated_daemon_stop_exit'] = stopped.returncode
                    if stopped.returncode != 0 or (native_home / 'daemon.pid').exists():
                        raise RuntimeError('The private native daemon did not stop cleanly.')
                is_cleanup_complete = all(process.poll() is not None for process in PROCESS_HANDLES)
            finally:
                for process, record in zip(PROCESS_HANDLES, PROCESS_RECORDS):
                    record['exit_code'] = process.poll()
                proof['private_processes'] = PROCESS_RECORDS
                proof['elapsed_seconds'] = time.monotonic() - STARTED
                proof['cleanup_completed'] = is_cleanup_complete
                proof['isolated_configured_daemon_proof_passed'] &= is_cleanup_complete and proof['elapsed_seconds'] <= 1200
                write(root / 'proof.json', json.dumps(proof, indent=2))
    if not proof['isolated_configured_daemon_proof_passed']:
        raise RuntimeError('The configured-daemon proof did not pass.')
    print(json.dumps(proof, indent=2), flush=True)


SENTINEL = r'''
import json, os, pathlib, subprocess, sys
root = pathlib.Path(os.environ['PROOF_ROOT'])
tool, args = sys.argv[1], sys.argv[2:]
if tool == 'isolation':
    observed = {'environment': os.environ.get('UV_PROJECT_ENVIRONMENT'), 'no_sync': os.environ.get('UV_NO_SYNC'), 'client_token': os.environ.get('ARBITRARY_CLIENT_TOKEN')}
    (root/'isolation.json').write_text(json.dumps(observed))
    print('unconfigured independent project '+json.dumps(observed), flush=True)
    sys.exit(0)
expected = ['run', '--extra', 'dev', 'pytest'] if tool == 'uv' else ['--test', 'tests/js/*.test.mjs']
safe = (args == expected and os.environ.get('UV_NO_SYNC') == '1' and
        os.environ.get('UV_OFFLINE') == '1' and os.environ.get('UV_PYTHON_DOWNLOADS') == 'never' and
        os.environ.get('UV_PYTHON') and os.environ.get('UV_PROJECT_ENVIRONMENT'))
if not safe:
    (root/'resolver-install-download-attempted').write_text('unsafe command or environment refused')
    sys.exit(98)
probe = subprocess.run([os.environ['UV_PYTHON'], '-I', '-c',
    'import json,sys;print(json.dumps({"version":".".join(map(str,sys.version_info[:3])),"releaselevel":sys.version_info.releaselevel,"serial":sys.version_info.serial,"prefix":sys.prefix,"executable":sys.executable}))'],
    check=True, capture_output=True, text=True, creationflags=0x08000000)
identity = json.loads(probe.stdout)
scenario = pathlib.Path('feature.txt').read_text().strip() if pathlib.Path('feature.txt').exists() else 'base'
head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True, creationflags=0x08000000).strip()
identity.update(tool=tool, args=args, head=head, client_token=os.environ.get('ARBITRARY_CLIENT_TOKEN'),
                gate_has_venv=pathlib.Path('.venv').exists(), gate_cwd=str(pathlib.Path.cwd()))
with (root/'events'/(scenario+'.jsonl')).open('a') as target:
    target.write(json.dumps(identity)+'\n')
print('configured '+tool+' sentinel '+json.dumps(identity), flush=True)
sys.exit(int((root/'exit-code.txt').read_text()) if tool == 'uv' else 0)
'''


if __name__ == '__main__':
    main()

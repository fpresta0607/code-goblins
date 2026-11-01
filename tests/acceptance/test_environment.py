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


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding='utf-8')


def run(arguments, directory, environment, check=True):
    return subprocess.run([str(value) for value in arguments], cwd=directory, env=environment,
                          capture_output=True, text=True, timeout=180, check=check,
                          creationflags=CREATE_NO_WINDOW)


def native_result(home, branch):
    with sqlite3.connect(f'file:{home / "state.sqlite"}?mode=ro', uri=True) as database:
        database.row_factory = sqlite3.Row
        record = database.execute('SELECT * FROM runs WHERE branch=? ORDER BY created_at DESC LIMIT 1', (branch,)).fetchone()
        if record is None:
            raise RuntimeError('The real daemon did not record the fixture run: ' + branch)
        test = database.execute('SELECT status,exit_code,findings_json FROM step_results WHERE run_id=? AND step_name=?',
                                (record['id'], 'test')).fetchone()
        return dict(record), dict(test) if test is not None else None


def events(root, scenario):
    path = root / 'events' / (scenario + '.jsonl')
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def main():
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
             'interpreter_download_attempted': False}
    daemon_pid, holders = None, []
    try:
        initialized = run([native, 'init'], checkout, environment)
        write(root / 'init.log', initialized.stdout + initialized.stderr)
        daemon_pid = json.loads((native_home / 'daemon.pid').read_text())['pid']
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
                    for _ in range(300):
                        line = json.loads(run([consumer, 'gate', 'turns', '--json'], root, environment).stdout)
                        registered = sum('admission holder ' in entry['who'] for entry in line['holding'] + line['waiting'])
                        if any((root / ('holder-started-' + str(number))).exists() for number in range(2)) and registered == 2:
                            break
                        time.sleep(.1)
                    else:
                        raise RuntimeError('The admission holder received no turn.')
                    driving = subprocess.Popen(command, cwd=owner, env=client_environment, stdout=drive_log,
                                               stderr=subprocess.STDOUT, creationflags=CREATE_NO_WINDOW)
                    try:
                        for _ in range(120):
                            line = json.loads(run([consumer, 'gate', 'turns', '--json'], root, environment).stdout)
                            if any('binding-admission' in waiting['who'] for waiting in line['waiting']):
                                break
                            if driving.poll() is not None:
                                raise RuntimeError('The native admission case ended before its wrapper waited.')
                            time.sleep(.25)
                        else:
                            raise RuntimeError('The real native wrapper did not join the admission line.')
                        (owner / '.venv').rename(owner / '.venv-unavailable')
                        (root / 'holder-release').touch()
                        for holder in holders:
                            if holder.wait(timeout=30) != 0:
                                raise RuntimeError('An admission holder failed.')
                        holders.clear()
                        drive_exit = driving.wait(timeout=180)
                    finally:
                        (root / 'holder-release').touch()
                        if driving.poll() is None:
                            driving.terminate()
                            driving.wait(timeout=30)
                        if (owner / '.venv-unavailable').exists():
                            (owner / '.venv-unavailable').rename(owner / '.venv')
            else:
                driven = run(command, owner, client_environment, check=False)
                drive_exit = driven.returncode
                write(root / (scenario + '-drive.log'), driven.stdout + driven.stderr)
            record, test = native_result(native_home, 'feat/binding-' + scenario)
            assert record['no_mistakes_build_sha'] == NATIVE_COMMIT, record['no_mistakes_build_sha']
            write(root / (scenario + '-run.json'), json.dumps(record, indent=2))
            write(root / (scenario + '-test.json'), json.dumps(test, indent=2))
            observed = events(root, scenario)
            proof[scenario + '_run'] = record['id']
            proof[scenario + '_drive_exit'] = drive_exit
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
        isolated = run([native, 'axi', 'run', '--intent-file', root / 'intent.txt', '--wait', '90s'], other, client_environment, check=False)
        write(root / 'isolation-drive.log', isolated.stdout + isolated.stderr)
        isolated_record, isolated_test = native_result(native_home, 'feat/isolation')
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
                    holder.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    proof['isolated_configured_daemon_proof_passed'] = False
                    holder.terminate()
                    holder.wait(timeout=30)
        finally:
            if daemon_pid is not None or (native_home / 'daemon.pid').exists():
                stopped = run([native, 'daemon', 'stop', '--force'], root, environment, check=False)
                write(root / 'daemon-stop.log', stopped.stdout + stopped.stderr)
                proof['isolated_daemon_stop_exit'] = stopped.returncode
                if stopped.returncode != 0:
                    proof['isolated_configured_daemon_proof_passed'] = False
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

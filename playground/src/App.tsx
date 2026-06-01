import React, { useState } from 'react';
import Editor from '@monaco-editor/react';

const LANGUAGES: Record<string, { label: string; defaultCode: string; hasStdin?: boolean }> = {
  py3: {
    label: 'Python 3',
    defaultCode: 'print("hello from python 3")',
    hasStdin: true,
  },
  c: {
    label: 'C',
    defaultCode: '#include <stdio.h>\nint main() {\n  printf("hello from c\\n");\n  return 0;\n}',
  },
  cpp: {
    label: 'C++',
    defaultCode: '#include <iostream>\nint main() {\n  std::cout << "hello from c++" << std::endl;\n  return 0;\n}',
  },
  java: {
    label: 'Java',
    defaultCode: 'public class Main {\n  public static void main(String[] args) {\n    System.out.println("hello from java");\n  }\n}',
  },
  bash: {
    label: 'Bash',
    defaultCode: 'echo "hello from bash"',
  },
  js: {
    label: 'JavaScript (Node)',
    defaultCode: 'console.log("hello from node");',
    hasStdin: true,
  },
  verilog: {
    label: 'Verilog',
    defaultCode: 'module hello;\ninitial begin\n  $display("hello from verilog");\n  $finish;\nend\nendmodule',
  },
  rust: {
    label: 'Rust',
    defaultCode: 'fn main() {\n  println!("hello from rust");\n}',
  },
  go: {
    label: 'Go',
    defaultCode: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello from go")\n}',
  },
};

const PRESETS: Record<string, Record<string, { source: string; stdin?: string; expected?: string }>> = {
  py3: {
    'hello world': { source: 'print("hello world")', stdin: '', expected: 'hello world\n' },
    'fibonacci': { source: 'def fib(n):\n  a, b = 0, 1\n  for _ in range(n):\n    print(a)\n    a, b = b, a+b\n\nfib(10)', stdin: '', expected: '' },
  },
  c: {
    'hello world': { source: '#include <stdio.h>\nint main() {\n  printf("hello world\\n");\n  return 0;\n}', stdin: '', expected: 'hello world\n' },
    'fibonacci': { source: '#include <stdio.h>\nint fib(int n) {\n  if (n <= 1) return n;\n  return fib(n-1) + fib(n-2);\n}\nint main() {\n  for (int i = 0; i < 10; i++)\n    printf("%d\\n", fib(i));\n  return 0;\n}', stdin: '', expected: '' },
  },
  cpp: {
    'hello world': { source: '#include <iostream>\nint main() {\n  std::cout << "hello world" << std::endl;\n  return 0;\n}', stdin: '', expected: 'hello world\n' },
  },
  java: {
    'hello world': { source: 'public class Main {\n  public static void main(String[] args) {\n    System.out.println("hello world");\n  }\n}', stdin: '', expected: 'hello world\n' },
  },
  bash: {
    'hello world': { source: 'echo "hello world"', stdin: '', expected: 'hello world\n' },
    'count': { source: 'for i in 1 2 3; do\necho "line $i"\ndone', stdin: '', expected: 'line 1\nline 2\nline 3\n' },
  },
  js: {
    'hello world': { source: 'console.log("hello world");', stdin: '', expected: 'hello world\n' },
  },
  verilog: {
    'hello world': { source: 'module hello;\ninitial begin\n  $display("hello world");\n  $finish;\nend\nendmodule', stdin: '', expected: 'hello world\n' },
  },
  rust: {
    'hello world': { source: 'fn main() {\n  println!("hello world");\n}', stdin: '', expected: 'hello world\n' },
  },
  go: {
    'hello world': { source: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello world")\n}', stdin: '', expected: 'hello world\n' },
  },
};

const API = window.location.origin;

function App() {
  const [lang, setLang] = useState('py3');
  const [code, setCode] = useState(LANGUAGES['py3'].defaultCode);
  const [stdin, setStdin] = useState('');
  const [output, setOutput] = useState('');
  const [loading, setLoading] = useState(false);
  const [presets, _] = useState(PRESETS);

  function switchLang(newLang: string) {
    setLang(newLang);
    setCode(LANGUAGES[newLang].defaultCode);
    setStdin('');
    setOutput('');
  }

  function loadPreset(name: string) {
    const p = presets[lang]?.[name];
    if (!p) return;
    setCode(p.source);
    if (p.stdin !== undefined) setStdin(p.stdin);
    setOutput('');
  }

  async function run() {
    setLoading(true);
    setOutput('');
    const body: any = { language: lang, source: code, tests: [{ stdin: stdin || '', expected_stdout: '' }] };
    try {
      const res = await fetch(API + '/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const data = await res.json();
      if (data.error) {
        setOutput('error: ' + data.error.message);
      } else {
        let out = 'status: ' + data.status + '\n';
        if (data.build) out += 'build: ' + data.build.status + ' (' + data.build.duration_ms + 'ms)\n';
        if (data.tests && data.tests.length > 0) {
          const t = data.tests[0];
          out += 'test status: ' + t.status + '\n';
          out += 'stdout: ' + t.stdout + '\n';
          if (t.stderr) out += 'stderr: ' + t.stderr + '\n';
          if (t.memory_peak_kb > 0) out += 'memory: ' + t.memory_peak_kb + ' KB\n';
        }
        setOutput(out);
      }
    } catch (e: any) {
      setOutput('request failed: ' + e.message);
    }
    setLoading(false);
  }

  return React.createElement('div', { style: { fontFamily: 'monospace', padding: '10px', maxWidth: '1200px', margin: '0 auto' } },
    React.createElement('h1', null, 'goboxd playground'),
    React.createElement('div', { style: { marginBottom: '10px', display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' } },
      React.createElement('label', null, 'language: '),
      React.createElement('select', { value: lang, onChange: (e: any) => switchLang(e.target.value), style: { fontSize: '14px' } },
        Object.entries(LANGUAGES).map(([id, l]) =>
          React.createElement('option', { key: id, value: id }, l.label)
        )
      ),
      React.createElement('label', null, ' preset: '),
      React.createElement('select', { onChange: (e: any) => e.target.value && loadPreset(e.target.value), defaultValue: '', style: { fontSize: '14px' } },
        React.createElement('option', { value: '', disabled: true }, 'select preset'),
        presets[lang] && Object.keys(presets[lang]).map(name =>
          React.createElement('option', { key: name, value: name }, name)
        )
      ),
    ),
    React.createElement('div', { style: { display: 'flex', gap: '10px', height: '500px' } },
      React.createElement('div', { style: { flex: 2, display: 'flex', flexDirection: 'column' } },
        React.createElement(Editor, {
          height: '100%',
          language: lang === 'c' ? 'c' : lang === 'cpp' ? 'cpp' : lang === 'java' ? 'java' : lang === 'js' ? 'javascript' : lang === 'bash' ? 'shell' : lang === 'rust' ? 'rust' : lang === 'go' ? 'go' : lang === 'verilog' ? 'verilog' : 'python',
          value: code,
          onChange: (v: string | undefined) => setCode(v || ''),
          theme: 'vs-dark',
          options: { minimap: { enabled: false }, fontSize: 14, lineNumbers: 'on' },
        }),
      ),
      React.createElement('div', { style: { flex: 1, display: 'flex', flexDirection: 'column', gap: '10px' } },
        LANGUAGES[lang]?.hasStdin && React.createElement('div', { style: { display: 'flex', flexDirection: 'column', gap: '5px' } },
          React.createElement('label', null, 'stdin:'),
          React.createElement('textarea', {
            value: stdin,
            onChange: (e: any) => setStdin(e.target.value),
            rows: 4,
            style: { fontFamily: 'monospace', fontSize: '13px' },
          }),
        ),
        React.createElement('button', {
          onClick: run,
          disabled: loading,
          style: { padding: '10px', fontSize: '16px', cursor: 'pointer', background: loading ? '#ccc' : '#4CAF50', color: 'white', border: 'none', borderRadius: '4px' },
        }, loading ? 'running...' : 'run'),
        React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '13px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px' } },
          output || 'output will appear here'),
      ),
    ),
    React.createElement('div', { style: { marginTop: '10px', color: '#888', fontSize: '12px' } },
      React.createElement('a', { href: API + '/info', target: '_blank' }, '/info'),
      ' | ',
      React.createElement('a', { href: API + '/readyz', target: '_blank' }, '/readyz'),
    ),
  );
}

export default App;

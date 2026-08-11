#!/usr/bin/env python3
import os
import re
import subprocess
import sys

DOCS_DIR = "/mnt/c/Users/USER/projects/relio/docs"

FILES = [
    ("ADMIN_GUIDE", "Relio 엔터프라이즈 관리자 가이드 (Admin Guide)", "v1.6.0-ENTERPRISE", "시스템 관리자, Security/DevOps"),
    ("USER_GUIDE", "Relio 영업대표 및 팀장 사용자 가이드 (User Guide)", "v1.6.0", "영업대표, 영업팀장, Presales"),
    ("EXECUTIVE_REPORT", "Relio 엔터프라이즈 도입 경영진 보고서 (Executive Summary)", "v1.6.0", "CEO, CIO, CISO, 영업총괄 VP"),
    ("ROADMAP_PLAN", "Relio 제품 로드맵 및 향후 발전 계획 (Roadmap Plan)", "v1.6.0", "Product Manager, Lead Dev, 경영진"),
    ("USER_GROUPS_ANALYSIS", "Relio 사용자 그룹 및 페르소나 분석 명세서 (User Analysis)", "v1.6.0", "UX Designer, PM, Business Analyst")
]

def markdown_to_html(text):
    # Escape HTML special chars inside code blocks first
    code_blocks = []
    def save_code_block(match):
        lang = match.group(1) or ""
        code = match.group(2)
        code_blocks.append((lang, code))
        return f"___CODE_BLOCK_{len(code_blocks)-1}___"
    
    text = re.sub(r'```(\w+)?\n(.*?)```', save_code_block, text, flags=re.DOTALL)
    
    # Inline code
    inline_codes = []
    def save_inline_code(match):
        inline_codes.append(match.group(1))
        return f"___INLINE_CODE_{len(inline_codes)-1}___"
    text = re.sub(r'`([^`]+)`', save_inline_code, text)

    lines = text.split('\n')
    out_lines = []
    in_list = False
    in_table = False
    table_headers = []

    for line in lines:
        line_str = line.strip()
        
        # Headers
        if line_str.startswith('# '):
            out_lines.append(f'<h1>{line_str[2:]}</h1>')
            continue
        elif line_str.startswith('## '):
            out_lines.append(f'<h2>{line_str[3:]}</h2>')
            continue
        elif line_str.startswith('### '):
            out_lines.append(f'<h3>{line_str[4:]}</h3>')
            continue
        elif line_str.startswith('#### '):
            out_lines.append(f'<h4>{line_str[5:]}</h4>')
            continue

        # Horizontal Rule
        if line_str == '---':
            out_lines.append('<hr>')
            continue

        # Blockquote
        if line_str.startswith('> '):
            out_lines.append(f'<blockquote>{line_str[2:]}</blockquote>')
            continue

        # List Items
        if line_str.startswith('- ') or line_str.startswith('* '):
            if not in_list:
                out_lines.append('<ul>')
                in_list = True
            out_lines.append(f'<li>{line_str[2:]}</li>')
            continue
        else:
            if in_list and not (line_str.startswith('- ') or line_str.startswith('* ')):
                out_lines.append('</ul>')
                in_list = False

        # Table processing
        if '|' in line_str and not line_str.startswith('___CODE_BLOCK'):
            parts = [p.strip() for p in line_str.split('|')[1:-1]]
            if all(set(p) <= set(':- ') for p in parts if p):
                continue # Separator line
            if not in_table:
                in_table = True
                out_lines.append('<div class="table-container"><table>')
                out_lines.append('<thead><tr>' + ''.join(f'<th>{p}</th>' for p in parts) + '</tr></thead><tbody>')
            else:
                out_lines.append('<tr>' + ''.join(f'<td>{p}</td>' for p in parts) + '</tr>')
            continue
        else:
            if in_table:
                out_lines.append('</tbody></table></div>')
                in_table = False

        if line_str == '':
            out_lines.append('')
            continue

        out_lines.append(f'<p>{line_str}</p>')

    if in_list:
        out_lines.append('</ul>')
    if in_table:
        out_lines.append('</tbody></table></div>')

    html_content = '\n'.join(out_lines)

    # Restore code blocks
    for i, (lang, code) in enumerate(code_blocks):
        escaped_code = code.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
        block_html = f'<pre><code class="{lang}">{escaped_code}</code></pre>'
        html_content = html_content.replace(f'___CODE_BLOCK_{i}___', block_html)

    # Restore inline code
    for i, code in enumerate(inline_codes):
        escaped_code = code.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
        html_content = html_content.replace(f'___INLINE_CODE_{i}___', f'<code>{escaped_code}</code>')

    # Bold and Links
    html_content = re.sub(r'\*\*([^*]+)\*\*', r'<strong>\1</strong>', html_content)
    html_content = re.sub(r'\[([^\]]+)\]\(([^)]+)\)', r'<a href="\2">\1</a>', html_content)

    return html_content

def build_full_html(title, version, target, content_html):
    return f"""<!DOCTYPE html>
<html lang="ko">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{title}</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=Plus+Jakarta+Sans:wght@600;700;800&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">
    <style>
        :root {{
            --primary: #2563eb;
            --primary-dark: #1d4ed8;
            --text-dark: #0f172a;
            --text-muted: #475569;
            --bg-light: #f8fafc;
            --border-color: #e2e8f0;
        }}
        * {{ box-sizing: border-box; margin: 0; padding: 0; }}
        body {{
            font-family: 'Inter', -apple-system, sans-serif;
            color: var(--text-dark);
            background: var(--bg-light);
            line-height: 1.7;
            padding: 40px 20px;
        }}
        .doc-card {{
            max-width: 1040px;
            margin: 0 auto;
            background: #ffffff;
            border: 1px solid var(--border-color);
            border-radius: 16px;
            padding: 56px;
            box-shadow: 0 10px 25px rgba(0,0,0,0.05);
        }}
        .header-meta {{
            border-bottom: 3px solid var(--primary);
            padding-bottom: 24px;
            margin-bottom: 36px;
        }}
        .header-meta h1 {{
            font-family: 'Plus Jakarta Sans', sans-serif;
            font-size: 2.1rem;
            color: #0f172a;
            margin-bottom: 16px;
            letter-spacing: -0.02em;
        }}
        .meta-grid {{
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 12px;
            font-size: 0.9rem;
            color: var(--text-muted);
        }}
        .meta-item {{
            background: #f1f5f9;
            padding: 10px 14px;
            border-radius: 8px;
            border: 1px solid #e2e8f0;
        }}
        h1 {{ display: none; }} /* Main H1 handled in header-meta */
        h2 {{
            font-family: 'Plus Jakarta Sans', sans-serif;
            font-size: 1.4rem;
            color: var(--primary-dark);
            margin: 40px 0 16px;
            border-bottom: 2px solid #f1f5f9;
            padding-bottom: 8px;
        }}
        h3 {{
            font-family: 'Plus Jakarta Sans', sans-serif;
            font-size: 1.15rem;
            color: #1e293b;
            margin: 24px 0 12px;
        }}
        h4 {{
            font-size: 1.02rem;
            color: #334155;
            margin: 18px 0 8px;
        }}
        p {{
            margin-bottom: 16px;
            font-size: 0.98rem;
            color: #334155;
        }}
        ul, ol {{
            margin-bottom: 20px;
            padding-left: 24px;
        }}
        li {{
            margin-bottom: 8px;
            font-size: 0.96rem;
            color: #334155;
        }}
        blockquote {{
            background: #eff6ff;
            border-left: 4px solid var(--primary);
            padding: 14px 18px;
            margin: 20px 0;
            border-radius: 0 8px 8px 0;
            font-size: 0.95rem;
            color: #1e3a8a;
        }}
        pre {{
            background: #0f172a;
            color: #f8fafc;
            padding: 20px;
            border-radius: 10px;
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.88rem;
            overflow-x: auto;
            margin: 20px 0;
            line-height: 1.5;
        }}
        code {{
            font-family: 'JetBrains Mono', monospace;
            background: #e2e8f0;
            color: #0f172a;
            padding: 2px 6px;
            border-radius: 4px;
            font-size: 0.88rem;
        }}
        pre code {{
            background: none;
            color: inherit;
            padding: 0;
        }}
        .table-container {{
            overflow-x: auto;
            margin: 24px 0;
            border: 1px solid var(--border-color);
            border-radius: 10px;
        }}
        table {{
            width: 100%;
            border-collapse: collapse;
            font-size: 0.92rem;
            text-align: left;
        }}
        th {{
            background: #f1f5f9;
            color: #0f172a;
            font-weight: 600;
            padding: 12px 16px;
            border-bottom: 2px solid var(--border-color);
        }}
        td {{
            padding: 12px 16px;
            border-bottom: 1px solid var(--border-color);
            color: #334155;
        }}
        tr:nth-child(even) {{
            background: #f8fafc;
        }}
        hr {{
            border: 0;
            height: 1px;
            background: var(--border-color);
            margin: 32px 0;
        }}
        .print-btn {{
            display: inline-block;
            background: var(--primary);
            color: #ffffff;
            padding: 10px 20px;
            border-radius: 8px;
            text-decoration: none;
            font-weight: 600;
            font-size: 0.9rem;
            float: right;
            transition: background 0.2s ease;
        }}
        .print-btn:hover {{
            background: var(--primary-dark);
        }}
        @media print {{
            .print-btn {{ display: none; }}
            body {{ background: #ffffff; padding: 0; }}
            .doc-card {{ border: none; box-shadow: none; padding: 0; max-width: 100%; }}
            pre {{ white-space: pre-wrap; word-break: break-all; }}
        }}
    </style>
</head>
<body>
    <div class="doc-card">
        <a href="javascript:window.print()" class="print-btn">🖨️ PDF / 인쇄하기</a>
        <div class="header-meta">
            <h1>{title}</h1>
            <div class="meta-grid">
                <div class="meta-item"><strong>문서 버전:</strong> {version}</div>
                <div class="meta-item"><strong>작성일자:</strong> 2026년 8월 11일</div>
                <div class="meta-item"><strong>대상 사용자:</strong> {target}</div>
                <div class="meta-item"><strong>구분:</strong> 공식 엔터프라이즈 문서</div>
            </div>
        </div>
        {content_html}
    </div>
</body>
</html>
"""

def generate_docs():
    for name, title, version, target in FILES:
        md_path = os.path.join(DOCS_DIR, f"{name}.md")
        html_path = os.path.join(DOCS_DIR, f"{name}.html")
        pdf_path = os.path.join(DOCS_DIR, f"{name}.pdf")

        if not os.path.exists(md_path):
            print(f"File not found: {md_path}")
            continue

        with open(md_path, 'r', encoding='utf-8') as f:
            md_text = f.read()

        content_html = markdown_to_html(md_text)
        full_html = build_full_html(title, version, target, content_html)

        with open(html_path, 'w', encoding='utf-8') as f:
            f.write(full_html)
        print(f"Generated HTML: {html_path}")

        # PDF Generation using chrome headless
        cmd = [
            "google-chrome",
            "--headless",
            "--no-sandbox",
            "--disable-gpu",
            f"--print-to-pdf={pdf_path}",
            html_path
        ]
        try:
            res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            if res.returncode == 0:
                print(f"Generated PDF: {pdf_path}")
            else:
                print(f"Failed PDF: {pdf_path}, Error: {res.stderr}")
        except Exception as e:
            print(f"PDF Exception: {e}")

if __name__ == "__main__":
    generate_docs()

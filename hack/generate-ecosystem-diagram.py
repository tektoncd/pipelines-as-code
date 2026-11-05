#!/usr/bin/env python3
"""Generate the PAC-centered Tekton ecosystem architecture diagram.

Outputs:
- docs/static/images/pac-ecosystem.svg (standalone SVG)
- docs/static/images/pac-ecosystem.png (high-res PNG via resvg if installed)
"""

import base64
import hashlib
import html
import math
import pathlib
import random
import shutil
import subprocess

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
ASSETS_DIR = REPO_ROOT / "docs" / "diagrams" / "assets"
IMAGES_DIR = REPO_ROOT / "docs" / "static" / "images"

OUT_SVG = IMAGES_DIR / "pac-ecosystem.svg"
OUT_PNG = IMAGES_DIR / "pac-ecosystem.png"

CANVAS_W = 1820
CANVAS_H = 1240


def build_excalidraw():
    random.seed(42)
    elements = []
    files = {}

    def next_seed():
        return random.randint(10000, 999999999)

    def register_file(name, filename, mime):
        path = ASSETS_DIR / filename
        b64 = base64.b64encode(path.read_bytes()).decode("ascii")
        file_id = hashlib.sha1(name.encode("utf-8")).hexdigest()[:20]
        files[file_id] = {
            "mimeType": mime,
            "id": file_id,
            "dataURL": f"data:{mime};base64,{b64}",
            "created": 1730800000000,
            "lastRetrieved": 1730800000000,
        }
        return file_id

    def add_rect(
        el_id,
        x,
        y,
        w,
        h,
        stroke="#1e1e1e",
        bg="transparent",
        fill_style="solid",
        stroke_width=2,
        stroke_style="solid",
        roughness=1,
        roundness=3,
        opacity=100,
    ):
        el = {
            "id": el_id,
            "type": "rectangle",
            "x": x,
            "y": y,
            "width": w,
            "height": h,
            "angle": 0,
            "strokeColor": stroke,
            "backgroundColor": bg,
            "fillStyle": fill_style,
            "strokeWidth": stroke_width,
            "strokeStyle": stroke_style,
            "roughness": roughness,
            "opacity": opacity,
            "groupIds": [],
            "frameId": None,
            "roundness": {"type": roundness} if roundness else None,
            "seed": next_seed(),
            "version": 1,
            "versionNonce": next_seed(),
            "isDeleted": False,
            "boundElements": [],
            "updated": 1730800000000,
            "link": None,
            "locked": False,
        }
        elements.append(el)
        return el

    def add_ellipse(
        el_id,
        x,
        y,
        w,
        h,
        stroke="#1e1e1e",
        bg="transparent",
        fill_style="solid",
        stroke_width=2,
        stroke_style="solid",
        roughness=1,
        opacity=100,
    ):
        el = {
            "id": el_id,
            "type": "ellipse",
            "x": x,
            "y": y,
            "width": w,
            "height": h,
            "angle": 0,
            "strokeColor": stroke,
            "backgroundColor": bg,
            "fillStyle": fill_style,
            "strokeWidth": stroke_width,
            "strokeStyle": stroke_style,
            "roughness": roughness,
            "opacity": opacity,
            "groupIds": [],
            "frameId": None,
            "roundness": {"type": 2},
            "seed": next_seed(),
            "version": 1,
            "versionNonce": next_seed(),
            "isDeleted": False,
            "boundElements": [],
            "updated": 1730800000000,
            "link": None,
            "locked": False,
        }
        elements.append(el)
        return el

    def add_text(
        el_id,
        x,
        y,
        text,
        font_size=20,
        color="#1e1e1e",
        align="left",
        font_family=2,
        w=None,
        h=None,
    ):
        lines = text.split("\n")
        max_chars = max(len(line) for line in lines)
        est_w = w if w is not None else int(max_chars * font_size * 0.56) + 8
        est_h = h if h is not None else int(len(lines) * font_size * 1.28)
        el = {
            "id": el_id,
            "type": "text",
            "x": x,
            "y": y,
            "width": est_w,
            "height": est_h,
            "angle": 0,
            "strokeColor": color,
            "backgroundColor": "transparent",
            "fillStyle": "solid",
            "strokeWidth": 1,
            "strokeStyle": "solid",
            "roughness": 1,
            "opacity": 100,
            "groupIds": [],
            "frameId": None,
            "roundness": None,
            "seed": next_seed(),
            "version": 1,
            "versionNonce": next_seed(),
            "isDeleted": False,
            "boundElements": [],
            "updated": 1730800000000,
            "link": None,
            "locked": False,
            "text": text,
            "fontSize": font_size,
            "fontFamily": font_family,
            "textAlign": align,
            "verticalAlign": "top",
            "baseline": int(font_size * 0.9),
            "containerId": None,
            "originalText": text,
            "lineHeight": 1.25,
        }
        elements.append(el)
        return el

    def add_image(el_id, x, y, w, h, file_id):
        el = {
            "id": el_id,
            "type": "image",
            "x": x,
            "y": y,
            "width": w,
            "height": h,
            "angle": 0,
            "strokeColor": "transparent",
            "backgroundColor": "transparent",
            "fillStyle": "solid",
            "strokeWidth": 1,
            "strokeStyle": "solid",
            "roughness": 0,
            "opacity": 100,
            "groupIds": [],
            "frameId": None,
            "roundness": None,
            "seed": next_seed(),
            "version": 1,
            "versionNonce": next_seed(),
            "isDeleted": False,
            "boundElements": [],
            "updated": 1730800000000,
            "link": None,
            "locked": False,
            "status": "saved",
            "fileId": file_id,
            "scale": [1, 1],
        }
        elements.append(el)
        return el

    def add_arrow(
        el_id,
        points,
        stroke="#1e1e1e",
        stroke_width=2,
        stroke_style="solid",
        start_arrow=None,
        end_arrow="arrow",
        roughness=1,
        roundness=2,
        label=None,
        label_offset=(0, 0),
        label_bg="#ffffff",
        label_color=None,
        label_size=15,
    ):
        x0, y0 = points[0]
        rel_pts = [[px - x0, py - y0] for px, py in points]
        xs = [p[0] for p in rel_pts]
        ys = [p[1] for p in rel_pts]
        w = max(xs) - min(xs)
        h = max(ys) - min(ys)
        el = {
            "id": el_id,
            "type": "arrow",
            "x": x0,
            "y": y0,
            "width": max(w, 1),
            "height": max(h, 1),
            "angle": 0,
            "strokeColor": stroke,
            "backgroundColor": "transparent",
            "fillStyle": "solid",
            "strokeWidth": stroke_width,
            "strokeStyle": stroke_style,
            "roughness": roughness,
            "opacity": 100,
            "groupIds": [],
            "frameId": None,
            "roundness": {"type": roundness} if roundness else None,
            "seed": next_seed(),
            "version": 1,
            "versionNonce": next_seed(),
            "isDeleted": False,
            "boundElements": [],
            "updated": 1730800000000,
            "link": None,
            "locked": False,
            "points": rel_pts,
            "lastCommittedPoint": None,
            "startBinding": None,
            "endBinding": None,
            "startArrowhead": start_arrow,
            "endArrowhead": end_arrow,
        }
        elements.append(el)

        if label:
            mid_idx = len(points) // 2
            if len(points) == 2:
                mx = (points[0][0] + points[1][0]) / 2.0
                my = (points[0][1] + points[1][1]) / 2.0
            else:
                mx = points[mid_idx][0]
                my = points[mid_idx][1]
            mx += label_offset[0]
            my += label_offset[1]
            lines = label.split("\n")
            max_c = max(len(line) for line in lines)
            lw = int(max_c * label_size * 0.56) + 18
            lh = int(len(lines) * label_size * 1.28) + 8
            add_rect(
                f"{el_id}_lbl_bg",
                mx - lw / 2,
                my - lh / 2,
                lw,
                lh,
                stroke="transparent",
                bg=label_bg,
                fill_style="solid",
                stroke_width=1,
                roughness=0,
                roundness=2,
                opacity=94,
            )
            add_text(
                f"{el_id}_lbl_txt",
                mx - lw / 2 + 8,
                my - lh / 2 + 4,
                label,
                font_size=label_size,
                color=label_color or stroke,
                align="center",
                w=lw - 16,
                h=lh - 8,
            )
        return el

    def add_person(
        prefix,
        cx,
        cy,
        label,
        ring_stroke="#2563eb",
        body_fill="#dbeafe",
        body_stroke="#1e40af",
    ):
        elements.append(
            {
                "id": f"{prefix}_avatar",
                "type": "person",
                "x": cx - 30,
                "y": cy - 30,
                "width": 60,
                "height": 60,
                "cx": cx,
                "cy": cy,
                "r": 30,
                "strokeColor": ring_stroke,
                "backgroundColor": "#ffffff",
                "bodyFill": body_fill,
                "bodyStroke": body_stroke,
                "strokeWidth": 2.2,
                "opacity": 100,
                "seed": next_seed(),
            }
        )
        add_text(
            f"{prefix}_txt",
            cx - 65,
            cy + 36,
            label,
            font_size=14,
            color="#1e293b",
            align="center",
            w=130,
        )

    def add_cylinder(prefix, x, y, w, h, stroke="#0f766e", bg="#f0fdfa", label=""):
        ry = 22
        add_rect(
            f"{prefix}_body",
            x,
            y + ry / 2,
            w,
            h - ry,
            stroke=stroke,
            bg=bg,
            fill_style="solid",
            stroke_width=2,
            roundness=None,
        )
        add_ellipse(
            f"{prefix}_bot",
            x,
            y + h - ry,
            w,
            ry,
            stroke=stroke,
            bg=bg,
            fill_style="solid",
            stroke_width=2,
        )
        add_ellipse(
            f"{prefix}_top",
            x,
            y,
            w,
            ry,
            stroke=stroke,
            bg="#ccfbf1",
            fill_style="solid",
            stroke_width=2,
        )
        if label:
            add_text(
                f"{prefix}_lbl",
                x + 10,
                y + ry + (h - ry * 1.5) / 2 - 10,
                label,
                font_size=16,
                color="#134e4a",
                align="center",
                w=w - 20,
            )

    def add_badge_pill(
        prefix,
        x,
        y,
        w,
        h,
        text,
        stroke="#6d28d9",
        bg="#ffffff",
        text_color="#4c1d95",
        font_size=14,
    ):
        add_rect(
            f"{prefix}_rect",
            x,
            y,
            w,
            h,
            stroke=stroke,
            bg=bg,
            fill_style="solid",
            stroke_width=1.8,
            roughness=0.8,
            roundness=3,
        )
        add_text(
            f"{prefix}_txt",
            x + 8,
            y + (h - font_size * 1.25) / 2,
            text,
            font_size=font_size,
            color=text_color,
            align="center",
            w=w - 16,
        )

    f_pac_badge = register_file("pac_badge", "pac_badge_hires.png", "image/png")
    f_tekton_main = register_file("tekton_main", "tekton_main.svg", "image/svg+xml")
    f_tekton_operator = register_file(
        "tekton_operator", "tekton_operator.png", "image/png"
    )
    f_tekton_cli = register_file("tekton_cli", "tekton_cli.svg", "image/svg+xml")
    f_tekton_dashboard = register_file(
        "tekton_dashboard", "tekton_dashboard.svg", "image/svg+xml"
    )
    f_tekton_chains = register_file(
        "tekton_chains", "tekton_chains.svg", "image/svg+xml"
    )
    f_tekton_results = register_file(
        "tekton_results", "tekton_results.svg", "image/svg+xml"
    )
    f_cloudevents = register_file("cloudevents", "cloudevents.svg", "image/svg+xml")
    f_artifact_hub = register_file("artifact_hub", "artifact_hub.svg", "image/svg+xml")
    f_opentelemetry = register_file(
        "opentelemetry", "opentelemetry.svg", "image/svg+xml"
    )
    f_spire = register_file("spire", "spire.svg", "image/svg+xml")
    f_spiffe = register_file("spiffe", "spiffe.svg", "image/svg+xml")
    f_sigstore = register_file("sigstore", "sigstore.svg", "image/svg+xml")
    f_in_toto = register_file("in_toto", "in_toto.svg", "image/svg+xml")
    f_github = register_file("github", "github.svg", "image/svg+xml")
    f_gitlab = register_file("gitlab", "gitlab.svg", "image/svg+xml")
    f_bitbucket = register_file("bitbucket", "bitbucket.svg", "image/svg+xml")
    f_forgejo = register_file("forgejo", "forgejo.svg", "image/svg+xml")

    # 1. TOP CENTER: GIT PROVIDERS
    add_rect(
        "git_providers_box",
        540,
        35,
        680,
        155,
        stroke="#1e293b",
        bg="#f8fafc",
        fill_style="solid",
        stroke_width=2.5,
        roughness=1.2,
    )
    add_text(
        "git_providers_title",
        560,
        48,
        "Git Providers",
        font_size=24,
        color="#0f172a",
        align="center",
        w=640,
    )
    add_text(
        "git_providers_sub",
        560,
        78,
        "Pull Requests • Push Events • Check Runs • Statuses • GitOps Comments",
        font_size=14,
        color="#475569",
        align="center",
        w=640,
    )

    git_items = [
        ("gh", "GitHub", f_github, 575),
        ("gl", "GitLab", f_gitlab, 735),
        ("fg", "Forgejo / Gitea", f_forgejo, 895),
        ("bb", "Bitbucket", f_bitbucket, 1065),
    ]
    for code, name, fid, gx in git_items:
        add_rect(
            f"git_card_{code}",
            gx,
            106,
            130,
            70,
            stroke="#cbd5e1",
            bg="#ffffff",
            fill_style="solid",
            stroke_width=1.5,
            roughness=0.8,
        )
        add_image(f"git_img_{code}", gx + 47, 112, 36, 36, fid)
        add_text(
            f"git_lbl_{code}",
            gx + 4,
            152,
            name,
            font_size=13,
            color="#1e293b",
            align="center",
            w=122,
        )

    # 2. TOP RIGHT: TEKTON OPERATOR
    add_rect(
        "operator_box",
        1360,
        45,
        390,
        140,
        stroke="#64748b",
        bg="#f1f5f9",
        fill_style="hachure",
        stroke_width=2.2,
        roughness=1.2,
    )
    add_text(
        "operator_title",
        1385,
        68,
        "Tekton Operator",
        font_size=24,
        color="#1e293b",
        align="left",
        w=220,
    )
    add_text(
        "operator_sub",
        1385,
        104,
        "Installs, configures &\nupgrades PAC & Tekton",
        font_size=15,
        color="#475569",
        align="left",
        w=220,
    )
    add_image("operator_logo", 1630, 56, 95, 118, f_tekton_operator)

    # 3. CENTER HERO: PIPELINES AS CODE (PAC)
    add_rect(
        "pac_halo",
        510,
        275,
        740,
        325,
        stroke="#c4b5fd",
        bg="#f5f3ff",
        fill_style="solid",
        stroke_width=1.5,
        roughness=0.8,
        opacity=70,
    )
    add_rect(
        "pac_box",
        520,
        285,
        720,
        305,
        stroke="#5b21b6",
        bg="#ede9fe",
        fill_style="solid",
        stroke_width=3.5,
        roughness=1.1,
    )
    add_text(
        "pac_title",
        550,
        305,
        "Pipelines as Code (PAC)",
        font_size=30,
        color="#3b0764",
        align="left",
        w=460,
    )
    add_text(
        "pac_subtitle",
        550,
        346,
        "Git-native CI/CD Engine & Controller for Tekton",
        font_size=16,
        color="#5b21b6",
        align="left",
        w=460,
    )
    add_image("pac_hero_logo", 1065, 298, 148, 148, f_pac_badge)
    add_text(
        "pac_logo_caption",
        1055,
        450,
        "TEKTON\nPIPELINES AS CODE",
        font_size=12,
        color="#4c1d95",
        align="center",
        w=168,
    )

    add_badge_pill(
        "pac_pill_1",
        545,
        385,
        240,
        44,
        ".tekton/ & CEL Matching",
        stroke="#6d28d9",
        bg="#ffffff",
        text_color="#3b0764",
        font_size=13,
    )
    add_badge_pill(
        "pac_pill_2",
        800,
        385,
        240,
        44,
        "GitOps (/retest, /cancel)",
        stroke="#6d28d9",
        bg="#ffffff",
        text_color="#3b0764",
        font_size=13,
    )
    add_badge_pill(
        "pac_pill_3",
        545,
        442,
        240,
        44,
        "Remote Task & Pipeline Resolver",
        stroke="#6d28d9",
        bg="#ffffff",
        text_color="#3b0764",
        font_size=13,
    )
    add_badge_pill(
        "pac_pill_4",
        800,
        442,
        240,
        44,
        "Concurrency & Queuing",
        stroke="#6d28d9",
        bg="#ffffff",
        text_color="#3b0764",
        font_size=13,
    )
    add_badge_pill(
        "pac_pill_5",
        545,
        500,
        495,
        44,
        "Controller • Watcher • Webhook  |  Repository CRD & Secret Auto-Management",
        stroke="#7c3aed",
        bg="#faf5ff",
        text_color="#4c1d95",
        font_size=12,
    )
    add_badge_pill(
        "pac_pill_6",
        1052,
        488,
        168,
        56,
        "AI / LLM\nFailure Analysis",
        stroke="#db2777",
        bg="#fdf2f8",
        text_color="#9d174d",
        font_size=13,
    )

    # 4. LEFT TOP: AUTHORING & CLI
    add_person(
        "user_author",
        62,
        330,
        "User, Author",
        ring_stroke="#2563eb",
        body_fill="#dbeafe",
        body_stroke="#1e40af",
    )
    add_arrow(
        "arr_user_cli",
        [(95, 330), (150, 330)],
        stroke="#1e293b",
        stroke_width=2,
    )
    add_rect(
        "cli_box",
        150,
        245,
        275,
        195,
        stroke="#2563eb",
        bg="#eff6ff",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "cli_title",
        166,
        264,
        "CLI & Authoring",
        font_size=19,
        color="#1e3a8a",
        align="left",
        w=155,
    )
    add_image("cli_logo", 316, 255, 100, 86, f_tekton_cli)
    add_badge_pill(
        "cli_pill_1",
        166,
        350,
        242,
        34,
        "tkn pac (bootstrap, create, resolve)",
        stroke="#3b82f6",
        bg="#ffffff",
        text_color="#1e40af",
        font_size=12,
    )
    add_badge_pill(
        "cli_pill_2",
        166,
        392,
        242,
        34,
        "IDE OpenAPI Schema & CEL Test",
        stroke="#3b82f6",
        bg="#ffffff",
        text_color="#1e40af",
        font_size=12,
    )

    # 5. LEFT BOTTOM: CATALOG & REMOTE TASKS
    add_person(
        "user_catalog",
        62,
        590,
        "Task Author",
        ring_stroke="#0891b2",
        body_fill="#cffafe",
        body_stroke="#155e75",
    )
    add_arrow(
        "arr_user_catalog",
        [(95, 590), (150, 590)],
        stroke="#1e293b",
        stroke_width=2,
    )
    add_rect(
        "catalog_box",
        150,
        500,
        275,
        205,
        stroke="#0284c7",
        bg="#f0f9ff",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "catalog_title",
        168,
        516,
        "Catalog & Remotes",
        font_size=22,
        color="#0c4a6e",
        align="left",
        w=240,
    )
    add_image("catalog_logo", 172, 550, 82, 88, f_artifact_hub)
    add_badge_pill(
        "catalog_pill_1",
        268,
        554,
        142,
        36,
        "Tekton Hub",
        stroke="#0284c7",
        bg="#ffffff",
        text_color="#075985",
        font_size=13,
    )
    add_badge_pill(
        "catalog_pill_2",
        268,
        598,
        142,
        36,
        "Remote Git Repos",
        stroke="#0284c7",
        bg="#ffffff",
        text_color="#075985",
        font_size=13,
    )
    add_badge_pill(
        "catalog_pill_3",
        168,
        652,
        242,
        34,
        "HTTP URLs & Custom Catalogs",
        stroke="#0284c7",
        bg="#ffffff",
        text_color="#075985",
        font_size=12,
    )

    # 6. RIGHT: OBSERVABILITY, CLOUDEVENTS & AI
    add_rect(
        "obs_box",
        1360,
        285,
        390,
        305,
        stroke="#d97706",
        bg="#fffbeb",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "obs_title",
        1382,
        302,
        "Observability, Events & AI",
        font_size=22,
        color="#78350f",
        align="left",
        w=350,
    )
    add_rect(
        "obs_ce_card",
        1382,
        345,
        165,
        105,
        stroke="#f59e0b",
        bg="#ffffff",
        fill_style="solid",
        stroke_width=1.5,
    )
    add_image("obs_ce_img", 1416, 353, 96, 62, f_cloudevents)
    add_text(
        "obs_ce_txt",
        1388,
        422,
        "CloudEvents",
        font_size=14,
        color="#92400e",
        align="center",
        w=152,
    )
    add_rect(
        "obs_otel_card",
        1562,
        345,
        168,
        105,
        stroke="#f59e0b",
        bg="#ffffff",
        fill_style="solid",
        stroke_width=1.5,
    )
    add_image("obs_otel_img", 1582, 353, 128, 65, f_opentelemetry)
    add_text(
        "obs_otel_txt",
        1568,
        422,
        "Metrics & Traces",
        font_size=14,
        color="#92400e",
        align="center",
        w=156,
    )
    add_rect(
        "obs_ai_card",
        1382,
        468,
        348,
        102,
        stroke="#ec4899",
        bg="#fdf2f8",
        fill_style="solid",
        stroke_width=1.8,
    )
    add_text(
        "obs_ai_title",
        1398,
        480,
        "AI / LLM Failure Analysis",
        font_size=17,
        color="#831843",
        align="center",
        w=316,
    )
    add_text(
        "obs_ai_sub",
        1398,
        508,
        "Configurable LLM\nAnalyzes failed task logs & posts PR summary",
        font_size=13,
        color="#9d174d",
        align="center",
        w=316,
    )

    # 7. BOTTOM CENTER: TEKTON PIPELINES
    add_rect(
        "pipeline_box",
        580,
        690,
        510,
        155,
        stroke="#be185d",
        bg="#fdf2f8",
        fill_style="solid",
        stroke_width=2.8,
        roughness=1.1,
    )
    add_text(
        "pipeline_title",
        608,
        712,
        "Tekton Pipelines",
        font_size=26,
        color="#831843",
        align="left",
        w=320,
    )
    add_text(
        "pipeline_sub",
        608,
        750,
        "Executes PipelineRuns & TaskRuns\non Kubernetes pods",
        font_size=15,
        color="#9d174d",
        align="left",
        w=320,
    )
    add_badge_pill(
        "pipeline_pill",
        608,
        794,
        310,
        34,
        "Isolated Workspaces • Matrix • Hermetic",
        stroke="#db2777",
        bg="#ffffff",
        text_color="#831843",
        font_size=12,
    )
    add_image("pipeline_logo", 965, 704, 102, 128, f_tekton_main)

    add_rect(
        "spire_box",
        1155,
        695,
        145,
        145,
        stroke="#334155",
        bg="#ffffff",
        fill_style="solid",
        stroke_width=2,
        stroke_style="dashed",
    )
    add_image("spire_img", 1222, 702, 68, 72, f_spire)
    add_image("spiffe_img", 1164, 764, 68, 68, f_spiffe)

    # 8. BOTTOM ROW: DASHBOARD & UIs (LEFT), CHAINS (CENTER), RESULTS (RIGHT)
    add_rect(
        "uis_box",
        130,
        915,
        340,
        155,
        stroke="#4f46e5",
        bg="#eef2ff",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "uis_title",
        152,
        932,
        "UIs & Consoles",
        font_size=22,
        color="#312e81",
        align="left",
        w=190,
    )
    add_text(
        "uis_sub",
        152,
        964,
        "Tekton Dashboard &\nOpenShift Console",
        font_size=14,
        color="#4338ca",
        align="left",
        w=185,
    )
    add_badge_pill(
        "uis_pill",
        152,
        1014,
        185,
        36,
        "Deep-linked Task Logs",
        stroke="#6366f1",
        bg="#ffffff",
        text_color="#312e81",
        font_size=13,
    )
    add_image("uis_logo", 345, 932, 112, 110, f_tekton_dashboard)

    add_rect(
        "chains_box",
        560,
        915,
        360,
        145,
        stroke="#7e22ce",
        bg="#faf5ff",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "chains_title",
        585,
        935,
        "Tekton Chains",
        font_size=24,
        color="#4c1d95",
        align="left",
        w=220,
    )
    add_text(
        "chains_sub",
        585,
        972,
        "Supply Chain Security\nSigns artifacts & provenance",
        font_size=14,
        color="#6b21a8",
        align="left",
        w=220,
    )
    add_image("chains_logo", 812, 924, 92, 118, f_tekton_chains)
    add_image("sigstore_img", 565, 1095, 115, 112, f_sigstore)
    add_rect(
        "in_toto_card",
        735,
        1102,
        175,
        98,
        stroke="#334155",
        bg="#ffffff",
        fill_style="solid",
        stroke_width=1.8,
    )
    add_image("in_toto_img", 772, 1110, 100, 82, f_in_toto)

    add_rect(
        "results_box",
        1010,
        915,
        360,
        145,
        stroke="#0f766e",
        bg="#f0fdfa",
        fill_style="solid",
        stroke_width=2.3,
        roughness=1.1,
    )
    add_text(
        "results_title",
        1035,
        935,
        "Tekton Results",
        font_size=24,
        color="#134e4a",
        align="left",
        w=220,
    )
    add_text(
        "results_sub",
        1035,
        972,
        "API & Archival Storage\nfor completed runs",
        font_size=14,
        color="#115e59",
        align="left",
        w=220,
    )
    add_image("results_logo", 1255, 928, 98, 108, f_tekton_results)

    add_cylinder(
        "history_db",
        1055,
        1105,
        270,
        95,
        stroke="#0f766e",
        bg="#f0fdfa",
        label="Long-term History DB",
    )

    add_person(
        "user_operator",
        1500,
        985,
        "User, Operator",
        ring_stroke="#0f766e",
        body_fill="#ccfbf1",
        body_stroke="#115e59",
    )
    add_arrow(
        "arr_operator_results",
        [(1460, 985), (1370, 985)],
        stroke="#1e293b",
        stroke_width=2,
        label="queries",
        label_offset=(0, -18),
    )

    # ARROWS & FLOW CONNECTIONS
    add_arrow(
        "arr_git_to_pac",
        [(760, 190), (760, 285)],
        stroke="#4c1d95",
        stroke_width=2.8,
        label="webhooks & events",
        label_offset=(-95, 0),
        label_bg="#f5f3ff",
        label_color="#4c1d95",
    )
    add_arrow(
        "arr_pac_to_git",
        [(990, 285), (990, 190)],
        stroke="#4c1d95",
        stroke_width=2.8,
        label="reports status, checks\n& PR comments",
        label_offset=(115, 0),
        label_bg="#f5f3ff",
        label_color="#4c1d95",
    )
    add_arrow(
        "arr_cli_to_git",
        [(285, 245), (285, 115), (540, 115)],
        stroke="#2563eb",
        stroke_width=2.2,
        label="pushes .tekton/ &\n/retest comments",
        label_offset=(95, -28),
        label_bg="#eff6ff",
        label_color="#1e40af",
    )
    add_arrow(
        "arr_cli_to_pac",
        [(425, 355), (520, 355)],
        stroke="#2563eb",
        stroke_width=2.2,
        label="tkn pac",
        label_offset=(0, -20),
        label_bg="#eff6ff",
        label_color="#1e40af",
    )
    add_arrow(
        "arr_catalog_to_pac",
        [(425, 525), (520, 480)],
        stroke="#0284c7",
        stroke_width=2.4,
        stroke_style="dashed",
        label="fetches remote\nTasks & Pipelines",
        label_offset=(-15, -34),
        label_bg="#f0f9ff",
        label_color="#075985",
    )
    add_arrow(
        "arr_op_to_pac",
        [(1360, 115), (1290, 115), (1290, 325), (1240, 325)],
        stroke="#64748b",
        stroke_width=2,
        stroke_style="dashed",
        label="installs & updates",
        label_offset=(-5, -105),
        label_bg="#f8fafc",
        label_color="#334155",
    )
    add_arrow(
        "arr_pac_to_ce",
        [(1240, 395), (1360, 395)],
        stroke="#d97706",
        stroke_width=2.2,
        label="metrics &\ntraces",
        label_offset=(0, -32),
        label_bg="#fffbeb",
        label_color="#92400e",
    )
    add_arrow(
        "arr_pac_to_ai",
        [(1240, 520), (1360, 520)],
        stroke="#db2777",
        stroke_width=2.2,
        start_arrow="arrow",
        end_arrow="arrow",
        label="LLM prompt\n& insight",
        label_offset=(0, 32),
        label_bg="#fdf2f8",
        label_color="#9d174d",
    )
    add_arrow(
        "arr_pac_to_pipeline",
        [(835, 590), (835, 690)],
        stroke="#be185d",
        stroke_width=3.2,
        label="creates, queues & manages\nPipelineRuns",
        label_offset=(125, 0),
        label_bg="#fdf2f8",
        label_color="#831843",
    )
    add_arrow(
        "arr_pipeline_to_otel",
        [(1055, 690), (1055, 642), (1645, 642), (1645, 590)],
        stroke="#d97706",
        stroke_width=2.2,
        label="metrics & traces",
        label_offset=(0, 0),
        label_bg="#fffbeb",
        label_color="#92400e",
    )
    add_arrow(
        "arr_pipeline_to_spire",
        [(1090, 770), (1155, 770)],
        stroke="#334155",
        stroke_width=2,
        label="uses",
        label_offset=(0, -18),
    )
    add_arrow(
        "arr_pac_to_uis",
        [(520, 565), (470, 565), (470, 775), (290, 775), (290, 915)],
        stroke="#4f46e5",
        stroke_width=2.2,
        stroke_style="dashed",
        label="links check-run & task log URLs",
        label_offset=(-90, 105),
        label_bg="#eef2ff",
        label_color="#312e81",
    )
    add_arrow(
        "arr_uis_to_pipeline",
        [(470, 965), (535, 810), (580, 810)],
        stroke="#4f46e5",
        stroke_width=2,
        label="shows",
        label_offset=(-25, -18),
        label_bg="#eef2ff",
        label_color="#312e81",
    )
    add_arrow(
        "arr_chains_watches",
        [(740, 915), (760, 845)],
        stroke="#7e22ce",
        stroke_width=2.2,
        label="watches",
        label_offset=(-45, 0),
        label_bg="#faf5ff",
        label_color="#581c87",
    )
    add_arrow(
        "arr_results_watches",
        [(1120, 915), (1020, 845)],
        stroke="#0f766e",
        stroke_width=2.2,
        label="watches",
        label_offset=(48, 0),
        label_bg="#f0fdfa",
        label_color="#134e4a",
    )
    add_arrow(
        "arr_chains_sigstore",
        [(655, 1060), (625, 1095)],
        stroke="#7e22ce",
        stroke_width=2,
        start_arrow="arrow",
        end_arrow="arrow",
    )
    add_arrow(
        "arr_chains_in_toto",
        [(820, 1060), (820, 1102)],
        stroke="#7e22ce",
        stroke_width=2,
    )
    add_arrow(
        "arr_results_db",
        [(1190, 1060), (1190, 1105)],
        stroke="#0f766e",
        stroke_width=2.2,
    )

    return {
        "type": "excalidraw",
        "version": 2,
        "source": "https://excalidraw.com",
        "elements": elements,
        "appState": {
            "gridSize": None,
            "viewBackgroundColor": "#ffffff",
        },
        "files": files,
    }


def hand_rect_paths(x, y, w, h, r, seed):
    rng = ((seed % 97) / 97.0) - 0.5
    d1 = rng * 1.4
    d2 = -rng * 1.2

    def rpath(ox, oy):
        rx = min(r, w / 2, h / 2)
        return (
            f"M {x + rx + ox:.1f} {y + oy:.1f} "
            f"L {x + w - rx - ox:.1f} {y - oy:.1f} "
            f"Q {x + w + ox:.1f} {y + oy:.1f} {x + w - ox:.1f} {y + rx + oy:.1f} "
            f"L {x + w + ox:.1f} {y + h - rx - oy:.1f} "
            f"Q {x + w - ox:.1f} {y + h - oy:.1f} {x + w - rx + ox:.1f} {y + h + oy:.1f} "
            f"L {x + rx - ox:.1f} {y + h - oy:.1f} "
            f"Q {x + ox:.1f} {y + h + oy:.1f} {x - ox:.1f} {y + h - rx + oy:.1f} "
            f"L {x + ox:.1f} {y + rx - oy:.1f} "
            f"Q {x - ox:.1f} {y - oy:.1f} {x + rx + ox:.1f} {y + oy:.1f} Z"
        )

    return rpath(d1, d2), rpath(d2 * 0.7, d1 * 0.7)


def arrow_head_poly(x1, y1, x2, y2, size=11):
    ang = math.atan2(y2 - y1, x2 - x1)
    a1 = ang + math.pi - 0.48
    a2 = ang + math.pi + 0.48
    p1x, p1y = x2 + size * math.cos(a1), y2 + size * math.sin(a1)
    p2x, p2y = x2 + size * math.cos(a2), y2 + size * math.sin(a2)
    return f"M {p1x:.1f} {p1y:.1f} L {x2:.1f} {y2:.1f} L {p2x:.1f} {p2y:.1f}"


def render_svg(doc):
    elements = doc["elements"]
    files = doc["files"]

    svg_parts = [
        (
            f'<svg id="diagram-svg" xmlns="http://www.w3.org/2000/svg" '
            f'viewBox="0 0 {CANVAS_W} {CANVAS_H}" width="{CANVAS_W}" height="{CANVAS_H}">'
        ),
        """<defs>
  <style>
    .diagram-text {
      font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
    }
  </style>
  <filter id="card-shadow" x="-6%" y="-6%" width="114%" height="116%">
    <feDropShadow dx="2" dy="4" stdDeviation="4" flood-color="#0f172a" flood-opacity="0.08"/>
  </filter>
  <filter id="hero-shadow" x="-8%" y="-8%" width="118%" height="120%">
    <feDropShadow dx="0" dy="6" stdDeviation="10" flood-color="#5b21b6" flood-opacity="0.18"/>
  </filter>
  <pattern id="hachure-slate" width="10" height="10" patternTransform="rotate(45 0 0)" patternUnits="userSpaceOnUse">
    <rect width="10" height="10" fill="#f8fafc"/>
    <line x1="0" y1="0" x2="0" y2="10" stroke="#e2e8f0" stroke-width="3"/>
  </pattern>
</defs>""",
        f'<rect x="0" y="0" width="{CANVAS_W}" height="{CANVAS_H}" fill="#ffffff"/>',
    ]

    for el in elements:
        etype = el["type"]
        el_id = el["id"]
        x, y, w, h = el["x"], el["y"], el["width"], el["height"]
        stroke = el.get("strokeColor", "#1e1e1e")
        bg = el.get("backgroundColor", "transparent")
        sw = el.get("strokeWidth", 2)
        sstyle = el.get("strokeStyle", "solid")
        opacity = el.get("opacity", 100) / 100.0
        dash = 'stroke-dasharray="8,6"' if sstyle == "dashed" else ""

        if etype == "rectangle":
            r = 14 if el.get("roundness") else 2
            if el_id.endswith("_lbl_bg"):
                r = 6
            fill = bg if bg != "transparent" else "none"
            if el.get("fillStyle") == "hachure" and bg != "transparent":
                fill = "url(#hachure-slate)"

            shadow = ""
            if el_id == "pac_box":
                shadow = 'filter="url(#hero-shadow)"'
            elif el_id.endswith("_box"):
                shadow = 'filter="url(#card-shadow)"'

            p1, p2 = hand_rect_paths(x, y, w, h, r, el["seed"])
            svg_parts.append(f'<g opacity="{opacity:.2f}" {shadow}>')
            if fill != "none":
                svg_parts.append(f'<path d="{p1}" fill="{fill}" stroke="none"/>')
            if stroke != "transparent":
                svg_parts.append(
                    f'<path d="{p1}" fill="none" stroke="{stroke}" '
                    f'stroke-width="{sw}" {dash} stroke-linejoin="round"/>'
                )
                if el.get("roughness", 1) > 0 and not dash:
                    svg_parts.append(
                        f'<path d="{p2}" fill="none" stroke="{stroke}" '
                        f'stroke-width="{sw * 0.65:.2f}" stroke-opacity="0.55" stroke-linejoin="round"/>'
                    )
            svg_parts.append("</g>")

        elif etype == "person":
            cx, cy, r = el["cx"], el["cy"], el["r"]
            body_fill = el.get("bodyFill", "#dbeafe")
            body_stroke = el.get("bodyStroke", "#1e40af")
            svg_parts.append('<g filter="url(#card-shadow)">')
            svg_parts.append(
                f'<circle cx="{cx:.1f}" cy="{cy:.1f}" r="{r:.1f}" '
                f'fill="{bg}" stroke="{stroke}" stroke-width="{sw}"/>'
            )
            svg_parts.append(
                f'<path d="M {cx - 16.5:.1f} {cy + 16.5:.1f} '
                f"C {cx - 16.5:.1f} {cy + 6.5:.1f}, {cx - 9.5:.1f} {cy + 2.5:.1f}, {cx:.1f} {cy + 2.5:.1f} "
                f"C {cx + 9.5:.1f} {cy + 2.5:.1f}, {cx + 16.5:.1f} {cy + 6.5:.1f}, {cx + 16.5:.1f} {cy + 16.5:.1f} "
                f'Q {cx:.1f} {cy + 19.5:.1f} {cx - 16.5:.1f} {cy + 16.5:.1f} Z" '
                f'fill="{body_fill}" stroke="{body_stroke}" stroke-width="2.2" stroke-linejoin="round"/>'
            )
            svg_parts.append(
                f'<circle cx="{cx:.1f}" cy="{cy - 9.5:.1f}" r="8.5" '
                f'fill="{body_fill}" stroke="{body_stroke}" stroke-width="2.2"/>'
            )
            svg_parts.append("</g>")

        elif etype == "ellipse":
            cx, cy = x + w / 2.0, y + h / 2.0
            rx, ry = w / 2.0, h / 2.0
            fill = bg if bg != "transparent" else "none"
            svg_parts.append(
                f'<ellipse cx="{cx:.1f}" cy="{cy:.1f}" rx="{rx:.1f}" ry="{ry:.1f}" '
                f'fill="{fill}" stroke="{stroke}" stroke-width="{sw}"/>'
            )
            svg_parts.append(
                f'<ellipse cx="{cx + 0.6:.1f}" cy="{cy - 0.5:.1f}" rx="{rx - 0.4:.1f}" ry="{ry + 0.4:.1f}" '
                f'fill="none" stroke="{stroke}" stroke-width="{sw * 0.6:.2f}" stroke-opacity="0.5"/>'
            )

        elif etype == "image":
            fid = el["fileId"]
            data_url = files[fid]["dataURL"]
            svg_parts.append(
                f'<image x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" '
                f'href="{data_url}" preserveAspectRatio="xMidYMid meet"/>'
            )

        elif etype == "arrow":
            pts = [[x + px, y + py] for px, py in el["points"]]
            if len(pts) == 2:
                mx = (pts[0][0] + pts[1][0]) / 2.0
                my = (pts[0][1] + pts[1][1]) / 2.0
                dx = pts[1][0] - pts[0][0]
                dy = pts[1][1] - pts[0][1]
                length = math.hypot(dx, dy) or 1.0
                nx, ny = -dy / length, dx / length
                d_str = (
                    f"M {pts[0][0]:.1f} {pts[0][1]:.1f} "
                    f"Q {mx + nx * 1.4:.1f} {my + ny * 1.4:.1f} {pts[1][0]:.1f} {pts[1][1]:.1f}"
                )
            else:
                segs = [f"M {pts[0][0]:.1f} {pts[0][1]:.1f}"]
                for i in range(1, len(pts) - 1):
                    p_prev = pts[i - 1]
                    p_curr = pts[i]
                    p_next = pts[i + 1]
                    dx1, dy1 = p_curr[0] - p_prev[0], p_curr[1] - p_prev[1]
                    dx2, dy2 = p_next[0] - p_curr[0], p_next[1] - p_curr[1]
                    len1 = math.hypot(dx1, dy1)
                    len2 = math.hypot(dx2, dy2)
                    rc = min(18.0, len1 * 0.4, len2 * 0.4)
                    if rc > 1:
                        bx = p_curr[0] - (dx1 / len1) * rc
                        by = p_curr[1] - (dy1 / len1) * rc
                        ax = p_curr[0] + (dx2 / len2) * rc
                        ay = p_curr[1] + (dy2 / len2) * rc
                        segs.append(
                            f"L {bx:.1f} {by:.1f} Q {p_curr[0]:.1f} {p_curr[1]:.1f} {ax:.1f} {ay:.1f}"
                        )
                    else:
                        segs.append(f"L {p_curr[0]:.1f} {p_curr[1]:.1f}")
                segs.append(f"L {pts[-1][0]:.1f} {pts[-1][1]:.1f}")
                d_str = " ".join(segs)

            svg_parts.append("<g>")
            svg_parts.append(
                f'<path d="{d_str}" fill="none" stroke="{stroke}" '
                f'stroke-width="{sw}" {dash} stroke-linecap="round" stroke-linejoin="round"/>'
            )
            if el.get("endArrowhead") == "arrow":
                ah = arrow_head_poly(
                    pts[-2][0], pts[-2][1], pts[-1][0], pts[-1][1], size=10 + sw * 1.2
                )
                svg_parts.append(
                    f'<path d="{ah}" fill="none" stroke="{stroke}" '
                    f'stroke-width="{sw}" stroke-linecap="round" stroke-linejoin="round"/>'
                )
            if el.get("startArrowhead") == "arrow":
                ah = arrow_head_poly(
                    pts[1][0], pts[1][1], pts[0][0], pts[0][1], size=10 + sw * 1.2
                )
                svg_parts.append(
                    f'<path d="{ah}" fill="none" stroke="{stroke}" '
                    f'stroke-width="{sw}" stroke-linecap="round" stroke-linejoin="round"/>'
                )
            svg_parts.append("</g>")

        elif etype == "text":
            fs = el["fontSize"]
            align = el.get("textAlign", "left")
            color = el.get("strokeColor", "#1e1e1e")
            lines = el["text"].split("\n")
            if align == "center":
                tx = x + w / 2.0
                anchor = "middle"
            elif align == "right":
                tx = x + w
                anchor = "end"
            else:
                tx = x
                anchor = "start"

            line_h = fs * 1.25
            fw = "700" if fs >= 20 or el_id.endswith("_title") else "500"
            for idx, line in enumerate(lines):
                ty = y + fs * 0.92 + idx * line_h
                esc = html.escape(line)
                svg_parts.append(
                    f'<text x="{tx:.1f}" y="{ty:.1f}" font-size="{fs}px" font-weight="{fw}" '
                    f'fill="{color}" text-anchor="{anchor}" class="diagram-text">{esc}</text>'
                )

    svg_parts.append("</svg>\n")
    OUT_SVG.write_text("\n".join(svg_parts), encoding="utf-8")


def render_png():
    resvg_bin = shutil.which("resvg")
    if not resvg_bin:
        print("resvg not found in PATH; skipping PNG generation (SVG updated).")
        return
    cmd = [
        resvg_bin,
        "--font-family",
        "Roboto",
        "--zoom",
        "2",
        str(OUT_SVG),
        str(OUT_PNG),
    ]
    subprocess.run(cmd, check=True)


def main():
    IMAGES_DIR.mkdir(parents=True, exist_ok=True)
    doc = build_excalidraw()
    render_svg(doc)
    render_png()
    print(f"Generated:\n  {OUT_SVG}\n  {OUT_PNG}")


if __name__ == "__main__":
    main()

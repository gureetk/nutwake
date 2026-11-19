"""The outage chart on the landing page: one power cut, drawn like the status page's recorder chart.

The data is the made-up story from `nutwake demo`. Positions are percentages of the time axis,
so the same markup scales to any width. Colours are CSS variables defined in assets/site.css.
"""

AMBER_FIXED = '#fab219'   # the "on battery" bar stays amber, with dark text, in both colour schemes
INK_FIXED = '#12202b'


def v(name):
    return f'var(--{name})'


INK, INK2, INK3, RULE = v('ink'), v('ink2'), v('ink3'), v('rule')
MAINS, SURFACE = v('mains'), v('surface')

DOM = 118.0          # minutes shown: 9:50 to 11:48


def P(t):
    return f'{t / DOM * 100:.3f}%'


def PW(a, b):
    return f'{(b - a) / DOM * 100:.3f}%'


T = dict(lost=14, shutdown=41, pioff=45, back=86, woken=97)   # minutes after 9:50

HATCH = (f'background-color:{v("gapa")};background-image:repeating-linear-gradient(135deg,'
         f'transparent 0 4px,{v("gapb")} 4px 5px);border:1px solid {v("offedge")};')


def seg(a, b, text, bg, fg, extra=''):
    return (f'<div style="position:absolute;left:{P(a)};width:{PW(a, b)};top:0;bottom:0;display:flex;'
            f'align-items:center;padding:0 8px;border-radius:3px;background:{bg};color:{fg};font-size:13px;'
            f'font-weight:500;white-space:nowrap;overflow:hidden;box-shadow:0 0 0 1px {SURFACE};{extra}">'
            f'{text}</div>')


def hatch(a, b, text='', top=0, bottom=0, align='flex-start'):
    inner = ''
    if text:
        inner = (f'<span style="padding:1px 8px;background:{SURFACE};border-radius:3px;font-size:13px;'
                 f'font-weight:500;white-space:nowrap;color:{v("onoff")}">{text}</span>')
    return (f'<div style="position:absolute;left:{P(a)};width:{PW(a, b)};top:{top}px;bottom:{bottom}px;'
            f'display:flex;align-items:center;justify-content:{align};padding:0 6px;border-radius:3px;'
            f'{HATCH}">{inner}</div>')


def row(label, sub, track, h=30):
    return (f'<div style="position:relative;display:flex;align-items:center;min-height:{h}px">'
            f'<div style="width:120px;flex:none;padding-right:12px">'
            f'<div style="font-size:14px;font-weight:600;line-height:1.25;color:{INK}">{label}</div>'
            f'<div style="font-size:13px;line-height:1.25;color:{INK3}">{sub}</div></div>'
            f'<div style="position:relative;flex:1;height:{h}px">{track}</div></div>')


def battery_track(plot_h=96, track_h=112, words=('wake nodes at 15%', 'shut down at 10%')):
    k = plot_h / 96

    def x(t):
        return t / DOM * 1000

    def y(p):
        return (100 - p) * 0.96 * k

    ys = lambda p: (100 - p) * 0.96     # path coordinates stay in the 0..96 viewBox
    pts = [(14, 100), (20.7, 85), (26.6, 70), (32.6, 45), (38.5, 20), (41.9, 10), (45, 10)]
    d1 = 'M0,0 ' + ' '.join(f'L{x(t):.1f},{ys(p):.1f}' for t, p in pts)
    a1 = d1 + f' L{x(45):.1f},96 L0,96 Z'
    d2 = f'M{x(86):.1f},{ys(9):.1f} L1000,{ys(20):.1f}'
    a2 = d2 + f' L1000,96 L{x(86):.1f},96 Z'
    guide = lambda top: (f'<div style="position:absolute;left:0;right:0;top:{top:.1f}px;border-top:1px dashed '
                         f'{v("guide")}"></div>')
    label = lambda top, text: (f'<div style="position:absolute;left:8px;top:{top:.1f}px;font-size:13px;'
                               f'line-height:16px;color:{INK3}">{text}</div>')
    return (
        f'<div style="position:relative;height:{track_h}px">'
        f'{guide(y(15))}{guide(y(10))}'
        f'<svg viewBox="0 0 1000 96" preserveAspectRatio="none" aria-hidden="true" '
        f'style="position:absolute;left:0;top:0;width:100%;height:{plot_h}px;overflow:visible">'
        f'<path d="{a1}" style="fill:{v("area")};stroke:none"/><path d="{a2}" style="fill:{v("area")};stroke:none"/>'
        f'<path d="{d1}" style="fill:none;stroke:{INK};stroke-width:2;stroke-linejoin:round;'
        f'vector-effect:non-scaling-stroke"/>'
        f'<path d="{d2}" style="fill:none;stroke:{INK};stroke-width:2;stroke-linejoin:round;'
        f'vector-effect:non-scaling-stroke"/></svg>'
        f'{hatch(45, 86, "not recorded", 0, track_h - plot_h, "center")}'
        f'{label(y(15) - 17, words[0])}{label(y(10) + 2, words[1])}'
        f'<div style="position:absolute;right:0;top:{y(20) - 24:.1f}px;font-size:13px;font-weight:700;'
        f'line-height:16px;color:{INK}">20%</div>'
        f'<div style="position:absolute;right:-4px;top:{y(20) - 4:.1f}px;width:8px;height:8px;'
        f'border-radius:50%;background:{INK}"></div></div>')


def marker_label(t, bold, when):
    return (f'<div style="position:absolute;left:{P(t)};top:0;height:48px;border-left:1px solid {v("marker")};'
            f'padding-left:5px;letter-spacing:-0.01em;line-height:1.3;white-space:nowrap">'
            f'<div style="font-size:13px;font-weight:700;color:{INK}">{bold}</div>'
            f'<div style="font-size:13px;color:{INK3}">{when}</div></div>')


def axis_tick(t, text):
    return (f'<div style="position:absolute;left:{P(t)};top:8px;transform:translateX(-50%);font-size:13px;'
            f'color:{INK3};white-space:nowrap">{text}</div>')


def segments():
    mains = (seg(0, T['lost'], 'On mains', MAINS, v('onmains'))
             + seg(T['lost'], T['pioff'], 'On battery', AMBER_FIXED, INK_FIXED)
             + hatch(T['pioff'], T['back'], 'Pi off', 0, 0)
             + seg(T['back'], DOM, 'On mains', MAINS, v('onmains')))
    up = lambda a, b: seg(a, b, 'Up', MAINS, v('onmains'))
    off = lambda a, b: seg(a, b, 'Off for 59 min', v('off'), v('onoff'),
                           f'border:1px solid {v("offedge")};box-shadow:none;')
    kaios = up(0, 42) + off(42, 101) + up(101, DOM)
    nodeb = up(0, 43) + off(43, 102) + up(102, DOM)
    pi = up(0, T['pioff']) + hatch(T['pioff'], T['back'], 'Off', 0, 0) + up(T['back'], DOM)
    return mains, kaios, nodeb, pi


BAND = (f'<div style="position:absolute;left:{P(T["lost"])};width:{PW(T["lost"], T["back"])};top:0;bottom:0;'
        f'background:{v("band")}"></div>')


def vline(k):
    return (f'<div style="position:absolute;top:0;bottom:0;left:{P(T[k])};width:1px;'
            f'background:{v("marker")}"></div>')


def chart_wide():
    mains, kaios, nodeb, pi = segments()
    labels = ''.join([
        marker_label(T['lost'], 'Power lost', '10:04 am'),
        marker_label(T['shutdown'], 'Shutdown starts', '10:31 am'),
        marker_label(T['back'], 'Power back', '11:16 am'),
        marker_label(T['woken'], 'Nodes woken', '11:27 am')])
    lines = ''.join(vline(k) for k in ('lost', 'shutdown', 'back', 'woken'))
    axis = ''.join([axis_tick(10, '10:00 am'), axis_tick(40, '10:30 am'),
                    axis_tick(70, '11:00 am'), axis_tick(100, '11:30 am')])
    return (
        f'<div class="chart-wide" style="overflow-x:auto"><div class="reveal" aria-hidden="true" '
        f'style="position:relative;min-width:980px;padding-right:6px">'
        f'<div style="display:flex"><div style="width:120px;flex:none"></div>'
        f'<div style="position:relative;flex:1;height:48px">{labels}</div></div>'
        f'<div style="position:relative;display:flex;flex-direction:column;gap:12px;padding:8px 0">'
        f'<div style="position:absolute;left:120px;right:0;top:0;bottom:0;pointer-events:none">{BAND}</div>'
        f'{row("Mains", "from the UPS", mains)}'
        f'{row("Battery", "charge", battery_track(), 112)}'
        f'{row("kaios", "10.10.0.10", kaios)}'
        f'{row("node-b", "10.10.0.11", nodeb)}'
        f'{row("This Pi", "127.0.0.1", pi)}'
        f'<div style="position:absolute;left:120px;right:0;top:0;bottom:0;pointer-events:none">{lines}</div>'
        f'</div>'
        f'<div style="display:flex;height:28px"><div style="width:120px;flex:none"></div>'
        f'<div style="position:relative;flex:1">{axis}</div></div>'
        f'</div></div>')


def nrow(label, sub, track, h=24):
    return (f'<div style="position:relative"><div style="display:flex;align-items:baseline;gap:8px;'
            f'margin-bottom:4px;font-size:13px;line-height:1.3;position:relative;z-index:2;'
            f'text-shadow:0 0 3px {SURFACE},0 0 3px {SURFACE},0 0 2px {SURFACE}"><span style="font-weight:600;color:{INK}">'
            f'{label}</span><span style="color:{INK3}">{sub}</span></div>'
            f'<div style="position:relative;height:{h}px">{track}</div></div>')


def seg_n(a, b, text, bg, fg, extra=''):
    """a bar for the compact chart: no label when the bar is too short to hold it"""
    return seg(a, b, text if (b - a) / DOM > 0.2 else '', bg, fg, extra)


def chart_narrow():
    """compact version for phone widths: rows stacked with their names above, three markers on two tiers"""
    up = lambda a, b: seg_n(a, b, 'Up', MAINS, v('onmains'))
    off = lambda a, b: seg_n(a, b, 'Off for 59 min', v('off'), v('onoff'),
                             f'border:1px solid {v("offedge")};box-shadow:none;')
    mains = (seg_n(0, T['lost'], 'On mains', MAINS, v('onmains'))
             + seg_n(T['lost'], T['pioff'], 'On battery', AMBER_FIXED, INK_FIXED)
             + hatch(T['pioff'], T['back'], 'Pi off', 0, 0)
             + seg_n(T['back'], DOM, 'On mains', MAINS, v('onmains')))
    kaios = up(0, 42) + off(42, 101) + up(101, DOM)
    nodeb = up(0, 43) + off(43, 102) + up(102, DOM)
    pi = up(0, T['pioff']) + hatch(T['pioff'], T['back'], 'Off', 0, 0) + up(T['back'], DOM)

    def mk(t, bold, when, top, height):
        return (f'<div style="position:absolute;left:{P(t)};top:{top}px;height:{height}px;'
                f'border-left:1px solid {v("marker")};padding-left:5px;line-height:1.3;white-space:nowrap">'
                f'<div style="font-size:13px;font-weight:700;color:{INK}">{bold}</div>'
                f'<div style="font-size:13px;color:{INK3}">{when}</div></div>')
    labels = (mk(T['lost'], 'Power lost', '10:04 am', 0, 78) + mk(T['back'], 'Power back', '11:16 am', 0, 78)
              + mk(T['shutdown'], 'Shutdown starts', '10:31 am', 42, 36))
    lines = ''.join(vline(k) for k in ('lost', 'shutdown', 'back'))
    axis = ''.join(
        f'<div style="position:absolute;left:{P(t)};top:8px;transform:translateX(-50%);font-size:13px;'
        f'color:{INK3};white-space:nowrap">{txt}</div>'
        for t, txt in ((10, '10:00'), (40, '10:30'), (70, '11:00'), (100, '11:30')))
    return (
        f'<div class="chart-narrow" aria-hidden="true" style="position:relative;padding-right:5px">'
        f'<div style="position:relative;height:78px">{labels}</div>'
        f'<div style="position:relative;display:flex;flex-direction:column;gap:14px;padding:8px 0">'
        f'<div style="position:absolute;left:0;right:0;top:0;bottom:0;pointer-events:none">{BAND}</div>'
        f'{nrow("Mains", "from the UPS", mains)}'
        f'{nrow("Battery", "charge", battery_track(72, 88, ("wake at 15%", "shut down at 10%")), 88)}'
        f'{nrow("kaios", "10.10.0.10", kaios)}{nrow("node-b", "10.10.0.11", nodeb)}'
        f'{nrow("This Pi", "127.0.0.1", pi)}'
        f'<div style="position:absolute;left:0;right:0;top:0;bottom:0;pointer-events:none">{lines}</div></div>'
        f'<div style="position:relative;height:28px">{axis}</div></div>')


def chart():
    return chart_wide() + chart_narrow()



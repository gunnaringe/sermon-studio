# Writes the ffmpeg aevalsrc expressions of the demo's music (make-jingles.sh): soft plucked arpeggios over a quiet
# pad, two seconds a chord, and a long final chord. Usage: python3 jingle.py <folder>
import sys
N = {'F3':174.61,'G3':196.00,'A3':220.00,'B3':246.94,'C4':261.63,'D4':293.66,'E4':329.63,'F4':349.23,'G4':392.00,'A4':440.00,'C5':523.25,'E5':659.25}
def pluck(f, t0, decay=1.6, amp=0.16):
    x = f"2*PI*{f}*(t-{t0})"
    env = f"exp(-{decay}*(t-{t0}))*min(1,(t-{t0})*200)"
    return f"gte(t,{t0})*{amp}*{env}*(sin({x})+0.35*sin(2*{x})+0.12*sin(3*{x}))"
def pad(fs, t0, t1, amp=0.035):
    env = f"min(1,(t-{t0})/0.6)*min(1,max(0,({t1}-t)/0.8))"
    return f"between(t,{t0},{t1})*{amp}*{env}*(" + "+".join(f"sin(2*PI*{f}*t)" for f in fs) + ")"
def piece(bars, final, final_t, length):
    parts = []
    for i,(chord,arp) in enumerate(bars):
        t0 = i*2.0
        parts.append(pad([N[n] for n in chord], t0, t0+2.4))
        for j,n in enumerate(arp):
            parts.append(pluck(N[n], round(t0 + j*0.5, 3)))
    parts.append(pad([N[n] for n in final], final_t, length, amp=0.04))
    for j,n in enumerate(final):
        parts.append(pluck(N[n], round(final_t + j*0.06, 3), decay=0.7, amp=0.12))
    return "+".join(parts)
intro = piece([(['C4','E4','G4'], ['C4','E4','G4','C5']), (['A3','C4','E4'], ['A3','C4','E4','A4']),
               (['F3','A3','C4'], ['F3','A3','C4','F4']), (['G3','B3','D4'], ['G3','B3','D4','G4'])],
              ['C4','E4','G4','C5'], 8.0, 12.0)
outro = piece([(['F3','A3','C4'], ['F4','C4','A3','F3']), (['G3','B3','D4'], ['G4','D4','B3','G3']),
               (['A3','C4','E4'], ['A4','E4','C4','A3']), (['F3','A3','C4'], ['F4','C4','A3','F3'])],
              ['C4','E4','G4','C5','E5'], 8.0, 14.0)
out = sys.argv[1]
open(out + '/intro.expr', 'w').write(intro)
open(out + '/outro.expr', 'w').write(outro)

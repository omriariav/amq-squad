# Reads one line from stdin. Prints only shape facts, never the value.
import sys
CH="qpzry9x8gf2tvdw0s3jn54khce6mua7l"
def polymod(v):
    G=[0x3b6a57b2,0x26508e6d,0x1ea119fa,0x3d4233dd,0x2a1462b3]; c=1
    for x in v:
        b=c>>25; c=((c&0x1ffffff)<<5)^x
        for i in range(5):
            c^= G[i] if (b>>i)&1 else 0
    return c
def hrp_exp(h): return [ord(c)>>5 for c in h]+[0]+[ord(c)&31 for c in h]
def valid(s):
    s=s.lower()
    if not s.startswith("nsec1") or len(s)!=63: return False
    d=s[5:]
    if any(c not in CH for c in d): return False
    return polymod(hrp_exp("nsec")+[CH.find(c) for c in d])==1
raw=sys.stdin.buffer.readline()
s=raw.decode("utf-8","replace").rstrip("\r\n")
print("chars=%d bytes=%d" % (len(s), len(raw.rstrip(b"\r\n"))))
bad=[(i,hex(ord(c))) for i,c in enumerate(s) if (i>=5 and c not in CH) or (i<5 and c!="nsec1"[i])]
print("non_bech32_positions=%s" % (bad[:5] if bad else "none"))
print("valid_as_is=%s" % valid(s))
fixes=[i for i in range(len(s)) if valid(s[:i]+s[i+1:])]
print("valid_if_char_removed_at=%s" % (fixes if fixes else "none"))

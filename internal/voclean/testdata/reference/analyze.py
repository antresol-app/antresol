import numpy as np, soundfile as sf
from scipy.signal import correlate
lf,sr=sf.read("lf.wav",dtype="float32"); en,_=sf.read("en.wav",dtype="float32")
names=["FL","FR","FC","LFE","SL","SR"]
# global lag via downmix envelope, 30 s window
a=lf[:sr*60].mean(1); b=en[:sr*60].mean(1)
c=correlate(a,b,mode="full",method="fft"); lag=int(np.argmax(c))-(len(b)-1)
print("lag samples",lag, "ms",lag/sr*1000)
if lag>0: lf=lf[lag:]
elif lag<0: en=en[-lag:]
n=min(len(lf),len(en)); lf,en=lf[:n],en[:n]
for ch in range(6):
    x,y=lf[:,ch],en[:,ch]
    g=float(np.dot(x,y)/np.dot(y,y)); r=x-g*y
    print(names[ch],"rms lf %.4f en %.4f  gain %.3f  residual/lf %.3f"%(np.sqrt((x*x).mean()),np.sqrt((y*y).mean()),g,np.sqrt((r*r).mean())/np.sqrt((x*x).mean())))
# per-second gain on FC to see ducking
x,y=lf[:,2],en[:,2]; gs=[]
for s in range(0,n-sr,sr):
    yy=y[s:s+sr]; d=np.dot(yy,yy)
    gs.append(np.dot(x[s:s+sr],yy)/d if d>1e-6 else np.nan)
gs=np.array(gs); print("FC per-second gain percentiles", np.nanpercentile(gs,[5,25,50,75,95]).round(3))
for ch in [0,4]:
    x,y=lf[:,ch],en[:,ch]; gs=[]
    for s in range(0,n-sr,sr):
        yy=y[s:s+sr]; d=np.dot(yy,yy); gs.append(np.dot(x[s:s+sr],yy)/d if d>1e-6 else np.nan)
    print(names[ch],"per-second gain pct",np.nanpercentile(gs,[5,50,95]).round(3))

# Reference subtraction: LostFilm centre = g(t,f) * original centre + Russian voices.
# Estimate g per band and ~0.3 s window by least squares, subtract in the complex STFT.
import sys, numpy as np, soundfile as sf
from scipy.signal import stft, istft
from scipy.ndimage import uniform_filter1d
sr=48000
lf,_=sf.read("lf.wav",dtype="float32"); en,_=sf.read("en.wav",dtype="float32")
x=lf[:,2].astype(np.float64); y=en[:,2].astype(np.float64)
nfft,hop=2048,512
f,t,X=stft(x,sr,nperseg=nfft,noverlap=nfft-hop); _,_,Y=stft(y,sr,nperseg=nfft,noverlap=nfft-hop)
nb=64; edges=np.unique(np.round(np.geomspace(1,X.shape[0],nb+1)).astype(int)); edges[0]=0
win=int(0.3*sr/hop)
G=np.zeros(X.shape)
for lo,hi in zip(edges[:-1],edges[1:]):
    num=np.real(X[lo:hi]*np.conj(Y[lo:hi])).sum(0); den=(np.abs(Y[lo:hi])**2).sum(0)
    num=uniform_filter1d(num,win); den=uniform_filter1d(den,win)
    g=np.clip(num/np.maximum(den,1e-12),0,1.5)
    G[lo:hi]=g
R=X-G*Y
_,ru=istft(R,sr,nperseg=nfft,noverlap=nfft-hop); ru=ru[:len(x)].astype(np.float32)
sf.write("ru_fc.wav",ru,sr,subtype="FLOAT")
np.save("gain.npy",G)
print("centre rms lf %.4f ru %.4f; median gain %.3f"%(np.sqrt((x**2).mean()),np.sqrt((ru**2).mean()),np.median(G)))

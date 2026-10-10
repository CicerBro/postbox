import{j as a,k as l}from"./index-Bb2iF1wR.js";import{B as o}from"./badge-gAKPFl4A.js";const p=`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body { font-family: Arial, sans-serif; }</style>
</head>
<body>
<p>Hello [Name,fallback=there],</p>
<p>Your newsletter goes here.</p>
<p><a href="[unsubscribe]">Unsubscribe</a> · <a href="[webversion]">View online</a></p>
</body>
</html>`,m="w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b",f="rounded-md border bg-background px-3 py-2 text-sm w-full",c=e=>e instanceof Error?e.message:"Request failed";function h({error:e}){return e?a.jsx("p",{role:"alert",className:"rounded-md border border-destructive p-3 text-sm text-destructive",children:c(e)}):null}async function b(e,n){let s=0;const r=[];for(;;){const t=await l(`${e}?after=${s}&limit=500`,{brandId:n});if(r.push(...t??[]),!t||t.length<500)return r;const i=t.at(-1).id;if(i<=s)throw new Error("Invalid pagination cursor");s=i}}const g=e=>new Intl.NumberFormat().format(e),x=e=>e>=1048576?`${(e/1048576).toFixed(1)} MB`:`${(e/1024).toFixed(1)} KB`;function w({status:e}){const n=["sent","delivered","completed","finished"].includes(e)?"success":["failed","cancelled","canceled"].includes(e)?"danger":["paused","unknown","scheduled"].includes(e)?"warning":["sending","preparing","claimed"].includes(e)?"info":"neutral";return a.jsxs(o,{variant:"outline",className:`status-badge status-${n}`,children:[a.jsx("span",{className:"status-dot","aria-hidden":"true"}),e]})}function y(e){return(e??"").replace(/<head[\s\S]*?<\/head>/gi," ").replace(/<style[\s\S]*?<\/style>/gi," ").replace(/<script[\s\S]*?<\/script>/gi," ").replace(/<[^>]*>/g," ").replace(/\s+/g," ").trim().slice(0,110)}export{h as E,w as S,b as a,x as b,y as c,p as i,g as n,f as s,m as t};

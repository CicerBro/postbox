import{j as o,i}from"./index-CmE_f31N.js";const c=`<!DOCTYPE html>
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
</html>`,m="w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b",u="rounded-md border bg-background px-3 py-2 text-sm w-full",l=e=>e instanceof Error?e.message:"Request failed";function f({error:e}){return e?o.jsx("p",{role:"alert",className:"rounded-md border border-destructive p-3 text-sm text-destructive",children:l(e)}):null}async function b(e,a){let s=0;const n=[];for(;;){const t=await i(`${e}?after=${s}&limit=500`,{brandId:a});if(n.push(...t??[]),!t||t.length<500)return n;const r=t.at(-1).id;if(r<=s)throw new Error("Invalid pagination cursor");s=r}}const p=e=>new Intl.NumberFormat().format(e),h=e=>e>=1048576?`${(e/1048576).toFixed(1)} MB`:`${(e/1024).toFixed(1)} KB`;export{f as E,b as a,h as b,c as i,p as n,u as s,m as t};

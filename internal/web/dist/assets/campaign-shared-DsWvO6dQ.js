import{j as t}from"./index-BTh0kfDe.js";import{B as s}from"./badge-Brrs9bHX.js";const r=`<!DOCTYPE html>
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
</html>`,l="w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b",c=e=>new Intl.NumberFormat().format(e),d=e=>e>=1048576?`${(e/1048576).toFixed(1)} MB`:`${(e/1024).toFixed(1)} KB`;function o({status:e}){const n=["sent","delivered","completed","finished"].includes(e)?"success":["failed","cancelled","canceled"].includes(e)?"danger":["paused","unknown","scheduled"].includes(e)?"warning":["sending","preparing","claimed"].includes(e)?"info":"neutral";return t.jsxs(s,{variant:"outline",className:`status-badge status-${n}`,children:[t.jsx("span",{className:"status-dot","aria-hidden":"true"}),e]})}function p(e){return(e??"").replace(/<head[\s\S]*?<\/head>/gi," ").replace(/<style[\s\S]*?<\/style>/gi," ").replace(/<script[\s\S]*?<\/script>/gi," ").replace(/<[^>]*>/g," ").replace(/\s+/g," ").trim().slice(0,110)}export{o as S,d as b,p as c,r as i,c as n,l as t};
